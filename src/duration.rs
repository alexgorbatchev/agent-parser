/// Parse Go-style durations in signed nanoseconds.
pub fn parse_go_duration(mut value: &str) -> Option<i64> {
    let negative = value.starts_with('-');
    value = value.strip_prefix(['-', '+']).unwrap_or(value);
    if value == "0" {
        return Some(0);
    }
    if value.is_empty() {
        return None;
    }

    let mut total = 0u64;
    while !value.is_empty() {
        let (integer, rest) = digits(value);
        let (fraction, rest) = rest.strip_prefix('.').map_or(("", rest), digits);
        if integer.is_empty() && fraction.is_empty() {
            return None;
        }
        let (unit, rest) = rest.split_at(
            rest.find(|c: char| c.is_ascii_digit() || c == '.')
                .unwrap_or(rest.len()),
        );
        let multiplier = match unit {
            "ns" => 1,
            "us" | "µs" | "μs" => 1_000,
            "ms" => 1_000_000,
            "s" => 1_000_000_000,
            "m" => 60_000_000_000,
            "h" => 3_600_000_000_000,
            _ => return None,
        };
        let integer = if integer.is_empty() {
            0
        } else {
            integer.parse::<u64>().ok()?
        };
        let whole = integer.checked_mul(multiplier)?;
        total = total
            .checked_add(whole)?
            .checked_add(fraction_ns(fraction, multiplier))?;
        value = rest;
    }
    if negative && total == 1 << 63 {
        return Some(i64::MIN);
    }
    let total = i64::try_from(total).ok()?;
    Some(if negative { -total } else { total })
}

fn digits(value: &str) -> (&str, &str) {
    let end = value.bytes().take_while(u8::is_ascii_digit).count();
    value.split_at(end)
}

fn fraction_ns(fraction: &str, multiplier: u64) -> u64 {
    let mut mantissa = 0;
    let mut scale = 1.0;
    for digit in fraction.bytes() {
        if mantissa > i64::MAX as u64 / 10 {
            break;
        }
        let next = mantissa * 10 + u64::from(digit - b'0');
        if next > 1 << 63 {
            break;
        }
        mantissa = next;
        scale *= 10.0;
    }
    (mantissa as f64 * (multiplier as f64 / scale)) as u64
}

#[cfg(test)]
mod tests {
    use super::parse_go_duration;

    #[test]
    fn accepts_unicode_and_fractional_go_units_without_panicking_on_bad_input() {
        for (input, expected) in [
            ("1µs1μs", Some(2_000)),
            ("+0", Some(0)),
            ("-.5h20m", Some(-3_000_000_000_000)),
            ("1m0.1s2ms3ns", Some(60_102_000_003)),
            ("-9223372036854775808ns", Some(i64::MIN)),
            ("0.9999999999999999999999999ns", Some(1)),
            ("9223372036854775808ns", None),
            ("99999999999999999h", None),
            ("1🐈", None),
            ("", None),
            (".s", None),
        ] {
            assert_eq!(parse_go_duration(input), expected, "{input}");
        }
    }
}
