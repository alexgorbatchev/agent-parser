use crate::{Image, common::s};
use base64::Engine;
use serde_json::Value;

pub(crate) fn codex(content: &Value, kind: &str) -> Vec<Image> {
    let Some(items) = content.as_array() else {
        return vec![];
    };
    let mut images = Vec::new();
    for item in items {
        if s(item, "type") != kind {
            continue;
        }
        let url = s(item, "image_url");
        let file_id = s(item, "file_id");
        if let Some((media_type, data)) = url
            .strip_prefix("data:")
            .and_then(|rest| rest.split_once(";base64,"))
        {
            images.push(inline(media_type, data));
        } else if !url.is_empty() {
            images.push(Image {
                url: url.into(),
                ..Default::default()
            });
        } else if !file_id.is_empty() {
            images.push(Image {
                file_id: file_id.into(),
                ..Default::default()
            });
        }
    }
    images
}

pub(crate) fn codex_generated(data: &str) -> Vec<Image> {
    if data.is_empty() {
        return vec![];
    }
    let head = &data.as_bytes()[..(data.len().min(64) & !3)];
    let Ok(head) = base64::engine::general_purpose::STANDARD.decode(head) else {
        return vec![];
    };
    let media_type = match head.as_slice() {
        [0x89, b'P', b'N', b'G', 0x0d, 0x0a, 0x1a, 0x0a, ..] => "image/png",
        [0xff, 0xd8, 0xff, ..] => "image/jpeg",
        [b'G', b'I', b'F', b'8', b'7' | b'9', b'a', ..] => "image/gif",
        [
            b'R',
            b'I',
            b'F',
            b'F',
            _,
            _,
            _,
            _,
            b'W',
            b'E',
            b'B',
            b'P',
            b'V',
            b'P',
            b'8',
            ..,
        ] => "image/webp",
        [b'B', b'M', ..] => "image/bmp",
        [0, 0, 1, 0, ..] => "image/x-icon",
        _ => "application/octet-stream",
    };
    vec![inline(media_type, data)]
}

pub(crate) fn claude(blocks: &[Value]) -> Vec<Image> {
    blocks
        .iter()
        .filter(|b| s(b, "type") == "image" && s(&b["source"], "type") == "base64")
        .map(|b| inline(s(&b["source"], "media_type"), s(&b["source"], "data")))
        .collect()
}

pub(crate) fn pi(content: &Value) -> Vec<Image> {
    let Some(blocks) = content.as_array() else {
        return vec![];
    };
    blocks
        .iter()
        .filter(|b| s(b, "type") == "image" && !s(b, "data").is_empty())
        .map(|b| inline(s(b, "mimeType"), s(b, "data")))
        .collect()
}

fn inline(media_type: &str, data: &str) -> Image {
    Image {
        media_type: media_type.into(),
        data: data.into(),
        ..Default::default()
    }
}
