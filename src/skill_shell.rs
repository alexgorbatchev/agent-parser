use super::{SkillInvocation, read_skill};
use regex::Regex;
use std::{collections::HashSet, sync::LazyLock};

const OPEN: char = '\u{e000}';
const CLOSE: char = '\u{e001}';
const COMMA: char = '\u{e002}';

pub(super) fn read_skills(script: &str) -> Vec<SkillInvocation> {
    static SEPARATORS: LazyLock<Regex> =
        LazyLock::new(|| Regex::new(r"&&|\|\||[;\n]").expect("valid command separator regex"));
    let mut seen = HashSet::new();
    let mut skills = vec![];
    for command in SEPARATORS.split(script) {
        if command.contains(['<', '>']) {
            continue;
        }
        let mut stages = command.split('|');
        let Some(fields) = stages.next().and_then(literal_fields) else {
            continue;
        };
        let Some((program, args)) = fields.split_first() else {
            continue;
        };
        if !matches!(
            basename(program),
            "cat" | "sed" | "head" | "nl" | "bat" | "less" | "more"
        ) || skips_first_line(&fields)
            || (basename(program) == "sed"
                && args
                    .iter()
                    .any(|arg| arg.starts_with("-i") || arg.starts_with("--in-place")))
            || stages
                .filter_map(literal_fields)
                .any(|stage| skips_first_line(&stage))
        {
            continue;
        }
        skills.extend(
            args.iter()
                .filter_map(|arg| read_skill(arg))
                .filter(|skill| seen.insert(skill.name.clone())),
        );
    }
    skills
}

fn basename(path: &str) -> &str {
    path.rsplit('/').next().unwrap_or(path)
}

fn skips_first_line(fields: &[String]) -> bool {
    static RANGE: LazyLock<Regex> =
        LazyLock::new(|| Regex::new(r"^([0-9]+)(?:,[0-9]+)?p$").expect("valid sed range regex"));
    match fields
        .split_first()
        .map(|(program, args)| (basename(program), args))
    {
        Some(("tail", _)) => true,
        Some(("sed", args)) => args
            .iter()
            .find_map(|arg| RANGE.captures(arg))
            .is_some_and(|range| &range[1] != "1"),
        _ => false,
    }
}

// shlex removes quoting and escapes. Mark only unquoted braces first so that
// quoted lists remain literal, and reject substitutions without evaluating them.
fn literal_fields(command: &str) -> Option<Vec<String>> {
    if command.contains([OPEN, CLOSE, COMMA]) {
        return None;
    }
    let mut marked = String::with_capacity(command.len());
    let mut quote = None;
    let mut characters = command.chars().peekable();
    while let Some(character) = characters.next() {
        match character {
            '\\' if quote != Some('\'') => {
                marked.push(character);
                marked.push(characters.next()?);
                continue;
            }
            '\'' | '"' if quote.is_none() || quote == Some(character) => {
                quote = if quote.is_none() {
                    Some(character)
                } else {
                    None
                };
            }
            '\x60' if quote != Some('\'') => return None,
            '$' if quote != Some('\'')
                && characters.peek().is_some_and(|next| {
                    next.is_ascii_alphanumeric() || "{(_@*#?$!-".contains(*next)
                }) =>
            {
                return None;
            }
            '(' | ')' | '&' if quote.is_none() => return None,
            _ => {}
        }
        marked.push(match (quote, character) {
            (None, '{') => OPEN,
            (None, '}') => CLOSE,
            (None, ',') => COMMA,
            _ => character,
        });
    }
    let mut fields = vec![];
    for word in shlex::split(&marked)? {
        expand_word(&word, &mut fields, 0)?;
    }
    Some(fields)
}

fn expand_word(word: &str, fields: &mut Vec<String>, depth: usize) -> Option<()> {
    if depth >= 16 || fields.len() >= 4096 {
        return None;
    }
    if let Some((open, close, separators)) = brace_group(word) {
        let mut from = open + OPEN.len_utf8();
        let suffix = &word[close + CLOSE.len_utf8()..];
        for to in separators.into_iter().chain([close]) {
            expand_word(
                &format!("{}{}{suffix}", &word[..open], &word[from..to]),
                fields,
                depth + 1,
            )?;
            from = to + COMMA.len_utf8();
        }
    } else {
        fields.push(
            word.replace(OPEN, "{")
                .replace(CLOSE, "}")
                .replace(COMMA, ","),
        );
    }
    Some(())
}

fn brace_group(word: &str) -> Option<(usize, usize, Vec<usize>)> {
    let mut groups = Vec::<(usize, Vec<usize>)>::new();
    let mut inner = None;
    for (index, character) in word.char_indices() {
        match character {
            OPEN => groups.push((index, vec![])),
            COMMA => {
                if let Some((_, separators)) = groups.last_mut() {
                    separators.push(index);
                }
            }
            CLOSE => {
                if let Some((start, separators)) = groups.pop() {
                    if !separators.is_empty() {
                        inner = Some((start, index, separators));
                    }
                    if groups.is_empty() && inner.is_some() {
                        return inner;
                    }
                }
            }
            _ => {}
        }
    }
    inner
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn tracks_literal_reads_from_the_start_and_deduplicates_names() {
        for (script, expected) in [
            (
                "cat /x/skills/{go,{rust,pi},go}/SKILL.md",
                vec!["go", "rust", "pi"],
            ),
            ("cat '/a dir/skills/{go,rust}/SKILL.md'", vec!["{go,rust}"]),
            (
                "git status && head -40 skills/.system/image/SKILL.md | nl",
                vec!["image"],
            ),
            (
                "nl skills/synced/abc/dev/SKILL.md | sed -n '1,30p'",
                vec!["dev"],
            ),
            ("cat skills/go/SKILL.md > out", vec![]),
            ("cat '$ROOT/skills/go/SKILL.md'", vec!["go"]),
            ("cat $ROOT/skills/go/SKILL.md", vec![]),
            ("cat $(pwd)/skills/go/SKILL.md", vec![]),
            ("cat \"skills/go/SKILL.md", vec![]),
            ("sed -i.bak 's/a/b/' skills/go/SKILL.md", vec![]),
            ("cat skills/go/SKILL.md | tail -20", vec![]),
            ("nl skills/go/SKILL.md | sed -n '120,300p'", vec![]),
            ("cat skills/user/go/SKILL.md", vec![]),
            ("git diff -- skills/go/SKILL.md", vec![]),
        ] {
            let skills = read_skills(script);
            let names: Vec<_> = skills.iter().map(|skill| skill.name.as_str()).collect();
            assert_eq!(names, expected, "{script}");
        }
    }
}
