package dlp

import (
	"context"
	"errors"
	"net"
	"strings"

	"github.com/company/sluice/internal/store"
)

// Built-in rule names. EnsureBuiltinRules matches existing rows by these
// names and never overwrites administrator tuning.
const (
	BuiltinIDCardName    = "中国大陆身份证号"
	BuiltinPhoneName     = "手机号"
	BuiltinBankCardName  = "银行卡号"
	BuiltinPrivateIPName = "内网 IP"
	BuiltinCloudKeyName  = "云厂商 AK/SK"
	BuiltinSecretName    = "通用密钥"
	BuiltinEmailName     = "邮箱地址"
)

// BuiltinRules returns the first-version default rule set.
func BuiltinRules() []store.DLPRule {
	return []store.DLPRule{
		{
			Name:        BuiltinIDCardName,
			Description: "中国大陆 18 位身份证号，含校验位",
			Category:    "pii",
			MatchType:   "regex",
			Pattern:     `\b\d{17}[\dXx]\b`,
			Action:      "mask",
			Replacement: "***",
			Priority:    10,
			Enabled:     true,
			Builtin:     true,
		},
		{
			Name:        BuiltinPhoneName,
			Description: "中国大陆 11 位手机号",
			Category:    "pii",
			MatchType:   "regex",
			Pattern:     `\b1[3-9]\d{9}\b`,
			Action:      "mask",
			Replacement: "***",
			Priority:    20,
			Enabled:     true,
			Builtin:     true,
		},
		{
			Name:        BuiltinBankCardName,
			Description: "16–19 位银行卡号，含 Luhn 校验",
			Category:    "pii",
			MatchType:   "regex",
			Pattern:     `\b\d{16,19}\b`,
			Action:      "mask",
			Replacement: "***",
			Priority:    30,
			Enabled:     true,
			Builtin:     true,
		},
		{
			Name:        BuiltinPrivateIPName,
			Description: "RFC1918 内网地址与 loopback 地址",
			Category:    "network",
			MatchType:   "regex",
			Pattern:     `\b(?:\d{1,3}\.){3}\d{1,3}\b`,
			Action:      "audit",
			Replacement: "***",
			Priority:    40,
			Enabled:     true,
			Builtin:     true,
		},
		{
			Name:        BuiltinCloudKeyName,
			Description: "AWS、阿里云、腾讯云常见访问密钥形态",
			Category:    "credentials",
			MatchType:   "regex",
			Pattern:     `(?:AKIA[0-9A-Z]{16}|LTAI[0-9A-Za-z]{12,}|AKID[0-9A-Za-z]{13,})`,
			Action:      "block",
			Replacement: "***",
			Priority:    50,
			Enabled:     true,
			Builtin:     true,
		},
		{
			Name:        BuiltinSecretName,
			Description: "通用 API Key 与私钥头",
			Category:    "credentials",
			MatchType:   "regex",
			Pattern:     `(?:sk-[A-Za-z0-9_-]{20,}|-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----|Bearer[ \t]+[A-Za-z0-9._~+/=-]{16,})`,
			Action:      "block",
			Replacement: "***",
			Priority:    60,
			Enabled:     true,
			Builtin:     true,
		},
		{
			Name:        BuiltinEmailName,
			Description: "邮箱地址",
			Category:    "contact",
			MatchType:   "regex",
			Pattern:     `\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`,
			Action:      "audit",
			Replacement: "***",
			Priority:    70,
			Enabled:     true,
			Builtin:     true,
		},
	}
}

// EnsureBuiltinRules inserts any missing built-in rules. Existing rows,
// including administrator-tuned ones, are left untouched.
func EnsureBuiltinRules(ctx context.Context, rules store.DLPRuleStore) error {
	existing, err := rules.List(ctx)
	if err != nil {
		return err
	}
	names := make(map[string]bool, len(existing))
	for _, rule := range existing {
		names[rule.Name] = true
	}
	for _, rule := range BuiltinRules() {
		if names[rule.Name] {
			continue
		}
		candidate := rule
		if err := rules.Create(ctx, &candidate); err != nil {
			if errors.Is(err, store.ErrConflict) {
				continue
			}
			return err
		}
	}
	return nil
}

func validatorFor(rule store.DLPRule) func(string) bool {
	if !rule.Builtin {
		return nil
	}
	switch rule.Name {
	case BuiltinIDCardName:
		return validIDCard
	case BuiltinBankCardName:
		return luhnValid
	case BuiltinPrivateIPName:
		return validPrivateIP
	default:
		return nil
	}
}

func validIDCard(value string) bool {
	if len(value) != 18 {
		return false
	}
	weights := [17]int{7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2}
	sum := 0
	for i := 0; i < 17; i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
		sum += int(value[i]-'0') * weights[i]
	}
	check := "10X98765432"[sum%11]
	return strings.ToUpper(value[17:]) == string(check)
}

func luhnValid(value string) bool {
	sum := 0
	double := false
	for i := len(value) - 1; i >= 0; i-- {
		digit := int(value[i] - '0')
		if digit < 0 || digit > 9 {
			return false
		}
		if double {
			digit *= 2
			if digit > 9 {
				digit -= 9
			}
		}
		sum += digit
		double = !double
	}
	return sum%10 == 0
}

func validPrivateIP(value string) bool {
	ip := net.ParseIP(value)
	return ip != nil && (ip.IsPrivate() || ip.IsLoopback())
}
