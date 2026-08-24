package internal

import (
	"errors"
	"math"
	"strings"
	"time"

	guardv1 "github.com/Muxcore-Media/playback-guard/proto/guardv1"
	"github.com/google/uuid"
)

var errInvalidRule = errors.New("invalid rule")

func newID() string {
	return uuid.NewString()
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func ruleTypeToString(t guardv1.RuleType) string {
	switch t {
	case guardv1.RuleType_RULE_TYPE_IMPOSSIBLE_TRAVEL:
		return "impossible_travel"
	case guardv1.RuleType_RULE_TYPE_SIMULTANEOUS_LOCATIONS:
		return "simultaneous_locations"
	case guardv1.RuleType_RULE_TYPE_DEVICE_VELOCITY:
		return "device_velocity"
	case guardv1.RuleType_RULE_TYPE_CONCURRENT_STREAMS:
		return "concurrent_streams"
	case guardv1.RuleType_RULE_TYPE_GEO_RESTRICTION:
		return "geo_restriction"
	case guardv1.RuleType_RULE_TYPE_ACCOUNT_INACTIVITY:
		return "account_inactivity"
	default:
		return "unspecified"
	}
}

func ruleTypeFromString(s string) guardv1.RuleType {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "impossible_travel":
		return guardv1.RuleType_RULE_TYPE_IMPOSSIBLE_TRAVEL
	case "simultaneous_locations":
		return guardv1.RuleType_RULE_TYPE_SIMULTANEOUS_LOCATIONS
	case "device_velocity":
		return guardv1.RuleType_RULE_TYPE_DEVICE_VELOCITY
	case "concurrent_streams":
		return guardv1.RuleType_RULE_TYPE_CONCURRENT_STREAMS
	case "geo_restriction":
		return guardv1.RuleType_RULE_TYPE_GEO_RESTRICTION
	case "account_inactivity":
		return guardv1.RuleType_RULE_TYPE_ACCOUNT_INACTIVITY
	default:
		return guardv1.RuleType_RULE_TYPE_UNSPECIFIED
	}
}

func timeParseRFC3339(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, s)
}

func clampInt32(n int) int32 {
	if n > math.MaxInt32 {
		return math.MaxInt32
	}
	if n < math.MinInt32 {
		return math.MinInt32
	}
	return int32(n)
}

func clampInt64ToInt32(n int64) int32 {
	if n > math.MaxInt32 {
		return math.MaxInt32
	}
	if n < math.MinInt32 {
		return math.MinInt32
	}
	return int32(n)
}
