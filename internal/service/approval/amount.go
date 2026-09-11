package approval

import (
	"fmt"
	"strings"
)

// FormatAmount renders a minor-unit amount as display text for notification
// payloads. IDR amounts are stored in rupiah (no practical subunit), so the
// value is grouped without decimals; other currencies render as "<SYM> <raw>"
// until multi-currency display rules exist (D5 keeps IDR the only accepted
// currency today).
func FormatAmount(amount int64, currency string) string {
	switch currency {
	case "IDR":
		return "Rp" + groupDigits(amount)
	case "USD":
		return "$" + groupDigits(amount)
	default:
		return fmt.Sprintf("%s %d", currency, amount)
	}
}

// groupDigits inserts thousands separators: 1500000 -> "1.500.000".
func groupDigits(n int64) string {
	sign := ""
	if n < 0 {
		sign = "-"
		n = -n
	}
	digits := fmt.Sprintf("%d", n)
	var b strings.Builder
	pre := len(digits) % 3
	if pre > 0 {
		b.WriteString(digits[:pre])
		if len(digits) > pre {
			b.WriteString(".")
		}
	}
	for i := pre; i < len(digits); i += 3 {
		b.WriteString(digits[i : i+3])
		if i+3 < len(digits) {
			b.WriteString(".")
		}
	}
	return sign + b.String()
}
