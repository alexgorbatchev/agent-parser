use super::{LiveParser, live_event, required_payload, sum_usage, update_text};
use crate::common::{n, opt, s};
use crate::{Error, EventData, ParsedEvent, TokenUsage, normalize_file_modification};
use serde::Deserialize;
use serde_json::Value;

impl LiveParser {
    pub(super) fn agy(&mut self, kind: &str, raw: &Value) -> Result<Vec<ParsedEvent>, Error> {
        update_text(&mut self.result.session_id, s(raw, "conversation_id"));
        match kind {
            "init" => {
                let init = &raw["init"];
                if !init.is_null() {
                    required_payload(raw, "init")?;
                }
                let model = s(init, "model");
                update_text(&mut self.result.model, model);
                let mut event = live_event(
                    "run_started",
                    EventData {
                        model: opt(model),
                        ..Default::default()
                    },
                );
                event.cwd = s(init, "cwd").into();
                Ok(vec![event])
            }
            "step_update" if !raw["step_update"].is_null() => {
                Ok(self.agy_step(required_payload(raw, "step_update")?))
            }
            "result" => {
                self.agy_result(required_payload(raw, "result")?)?;
                Ok(self.finish())
            }
            _ => Ok(vec![]),
        }
    }

    fn agy_step(&mut self, step: &Value) -> Vec<ParsedEvent> {
        update_text(&mut self.result.session_id, s(step, "conversation_id"));
        match s(step, "step_type") {
            "user_input" => {
                self.ended = false;
                vec![live_event("turn_start", EventData::default())]
            }
            "agent_response" => [
                ("thinking_delta", "thinking_delta"),
                ("agent_message_delta", "text_delta"),
            ]
            .into_iter()
            .filter_map(|(kind, field)| {
                opt(s(step, field)).map(|delta| {
                    live_event(
                        kind,
                        EventData {
                            delta: Some(delta),
                            native: Some(step.clone()),
                            ..Default::default()
                        },
                    )
                })
            })
            .collect(),
            "tool" => self.agy_tool(step),
            _ => vec![],
        }
    }

    fn agy_tool(&mut self, step: &Value) -> Vec<ParsedEvent> {
        let index = n(step, "step_index");
        let tool = &step["tool_info"];
        let name = opt(s(step, "tool_name"))
            .or_else(|| opt(s(tool, "name")))
            .or_else(|| (!step["subagent_info"].is_null()).then(|| "invoke_subagent".into()));
        let mut data = EventData {
            tool_call_id: Some(format!("step-{index}")),
            tool_name: name,
            native: Some(step.clone()),
            ..Default::default()
        };
        let mut events = Vec::new();
        if self.agy_tool_steps.insert(index) {
            let input = tool["parameters"].as_object().filter(|map| !map.is_empty());
            let file_modification = data
                .tool_name
                .as_deref()
                .and_then(|name| normalize_file_modification(name, input));
            events.push(live_event(
                "tool_call",
                EventData {
                    file_modification,
                    tool_input: input.cloned(),
                    ..data.clone()
                },
            ));
        }
        if s(step, "state") == "DONE" {
            data.is_error = Some(!tool["error"].is_null());
            data.tool_output = tool["output"]
                .as_str()
                .map(str::to_owned)
                .or_else(|| opt(s(&tool["error"], "message")));
            events.push(live_event("tool_result", data));
        } else if events.is_empty() {
            events.push(live_event("tool_update", data));
        }
        events
    }

    fn agy_result(&mut self, raw: &Value) -> Result<(), Error> {
        if !raw["usage"].is_null() {
            let native: AgyUsage = serde_json::from_value(raw["usage"].clone())?;
            self.result.usage = Some(native.normalized()?);
        }
        update_text(&mut self.result.session_id, s(raw, "conversation_id"));
        let status = s(raw, "status");
        self.result.native_subtype = status.into();
        self.result.is_error = status != "SUCCESS";
        self.result.error_message = s(raw, "error").into();
        let response = raw["response"].as_str();
        self.result.has_text = response.is_some_and(|text| !text.is_empty() || status == "SUCCESS");
        self.result.text = response.unwrap_or_default().into();
        self.result.structured_output = raw.get("structured_output").cloned();
        Ok(())
    }
}

#[derive(Default, Deserialize)]
#[serde(default)]
struct AgyUsage {
    input_tokens: i64,
    output_tokens: i64,
    thinking_tokens: i64,
    cache_read_tokens: i64,
    cache_creation_tokens: i64,
    total_tokens: i64,
}

impl AgyUsage {
    fn normalized(&self) -> Result<TokenUsage, Error> {
        let total = if self.total_tokens == 0 {
            sum_usage([
                self.input_tokens,
                self.output_tokens,
                self.cache_read_tokens,
                self.cache_creation_tokens,
            ])?
        } else {
            self.total_tokens
        };
        Ok(TokenUsage {
            input: self.input_tokens,
            output: self.output_tokens,
            reasoning: self.thinking_tokens,
            cache_read: self.cache_read_tokens,
            cache_write: self.cache_creation_tokens,
            total,
        })
    }
}
