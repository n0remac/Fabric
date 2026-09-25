package fabric

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

var ErrInvalidValue = errors.New("invalid bound value")

func FormatValue(value any, format ValueFormat, suffix string) (string, error) {
	var output string
	switch format {
	case "", FormatPlain:
		if value == nil {
			return "", fmt.Errorf("%w: nil", ErrInvalidValue)
		}
		output = fmt.Sprint(value)
	case FormatDuration:
		seconds, ok := Number(value)
		if !ok || seconds < 0 {
			return "", fmt.Errorf("%w: duration requires non-negative seconds", ErrInvalidValue)
		}
		output = formatDuration(int64(seconds))
	case FormatCurrency, FormatSigned, FormatPercent, FormatCompact:
		number, ok := Number(value)
		if !ok {
			return "", fmt.Errorf("%w: numeric format requires a number", ErrInvalidValue)
		}
		switch format {
		case FormatCurrency:
			output = fmt.Sprintf("$%.2f", number)
		case FormatSigned:
			output = fmt.Sprintf("%+.2f", number)
		case FormatPercent:
			output = fmt.Sprintf("%+.2f%%", number)
		case FormatCompact:
			magnitude, unit := 1.0, ""
			if math.Abs(number) >= 1e9 {
				magnitude, unit = 1e9, "B"
			} else if math.Abs(number) >= 1e6 {
				magnitude, unit = 1e6, "M"
			} else if math.Abs(number) >= 1e3 {
				magnitude, unit = 1e3, "K"
			}
			if unit == "" {
				output = fmt.Sprintf("%.0f", number)
			} else {
				output = fmt.Sprintf("%.1f%s", number/magnitude, unit)
			}
		}
	default:
		return "", fmt.Errorf("%w: unsupported format %q", ErrInvalidValue, format)
	}
	return output + suffix, nil
}

func Number(value any) (float64, bool) {
	var number float64
	switch typed := value.(type) {
	case int:
		number = float64(typed)
	case int8:
		number = float64(typed)
	case int16:
		number = float64(typed)
	case int32:
		number = float64(typed)
	case int64:
		number = float64(typed)
	case uint:
		number = float64(typed)
	case uint8:
		number = float64(typed)
	case uint16:
		number = float64(typed)
	case uint32:
		number = float64(typed)
	case uint64:
		number = float64(typed)
	case float32:
		number = float64(typed)
	case float64:
		number = typed
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		number = parsed
	case string:
		parsed, err := strconv.ParseFloat(typed, 64)
		if err != nil {
			return 0, false
		}
		number = parsed
	default:
		return 0, false
	}
	return number, !math.IsNaN(number) && !math.IsInf(number, 0)
}

func BoundDisplay(data map[string]any, component Component) string {
	value, err := ResolveBinding(data, component.Bind)
	if err != nil {
		return "—"
	}
	formatted, err := FormatValue(value, component.Format, component.Suffix)
	if err != nil {
		return "—"
	}
	return formatted
}

func formatDuration(total int64) string {
	if total < 60 {
		return fmt.Sprintf("%ds", total)
	}
	units := []struct {
		label string
		value int64
	}{{"d", 86400}, {"h", 3600}, {"m", 60}}
	parts := make([]string, 0, 2)
	for _, unit := range units {
		if amount := total / unit.value; amount > 0 {
			parts = append(parts, fmt.Sprintf("%d%s", amount, unit.label))
			total %= unit.value
			if len(parts) == 2 {
				break
			}
		}
	}
	return strings.Join(parts, " ")
}
