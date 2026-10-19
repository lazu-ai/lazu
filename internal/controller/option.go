package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/httperr"
	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/setting"
	"github.com/lazu-ai/lazu/internal/setting/console_setting"
	"github.com/lazu-ai/lazu/internal/types"

	"github.com/gin-gonic/gin"
)

func GetOptions(c *gin.Context) {
	var options []*model.Option
	common.OptionMapRWMutex.Lock()
	for k, v := range common.OptionMap {
		if strings.HasSuffix(k, "Token") || strings.HasSuffix(k, "Secret") || strings.HasSuffix(k, "Key") {
			continue
		}
		if model.IsIgnoredLegacyOption(k) {
			continue
		}
		options = append(options, &model.Option{
			Key:   k,
			Value: common.Interface2String(v),
		})
	}
	common.OptionMapRWMutex.Unlock()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    options,
	})
}

type OptionUpdateRequest struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

// Rules the option endpoint reports with setting_invalid, beyond the list
// rules console_setting defines.
const (
	// The switch cannot turn on while the list it enforces is empty.
	settingRuleRequiresValues = "requires_values"
	// The list cannot be emptied while the switch that enforces it is on.
	settingRuleLockedWhileEnabled = "locked_while_enabled"
	// The value must be an integer of at least details.limit.
	settingRuleMinInteger = "min_integer"
	// A whole number inside details.min … details.max inclusive.
	settingRuleIntegerRange = "integer_range"
)

// settingInvalid names the option key and the rule its value broke. The
// console turns (field, rule) into a sentence next to that field.
func settingInvalid(key string, rule string, extra ...types.D) *types.APIError {
	details := types.D{"field": key, "rule": rule}
	for _, d := range extra {
		for k, v := range d {
			details[k] = v
		}
	}
	return types.Err(types.ErrorCodeSettingInvalid, details)
}

// settingFormatInvalid keeps the parser's complaint in the server log and
// tells the admin only which field has a malformed value.
func settingFormatInvalid(key string, cause error) *types.APIError {
	return types.WrapErr(cause, types.ErrorCodeSettingInvalid, types.D{"field": key, "rule": console_setting.RuleFormat})
}

func UpdateOption(c *gin.Context) {
	var option OptionUpdateRequest
	if err := json.NewDecoder(c.Request.Body).Decode(&option); err != nil {
		httperr.Code(c, types.ErrorCodeInvalidRequest, types.D{"reason": "the body must be a JSON object with key and value"})
		return
	}
	switch option.Value.(type) {
	case bool:
		option.Value = common.Interface2String(option.Value.(bool))
	case float64:
		option.Value = common.Interface2String(option.Value.(float64))
	case int:
		option.Value = common.Interface2String(option.Value.(int))
	default:
		option.Value = fmt.Sprintf("%v", option.Value)
	}
	switch option.Key {
	case "EmailDomainRestrictionEnabled":
		if option.Value == "true" && len(common.EmailDomainWhitelist) == 0 {
			httperr.Write(c, settingInvalid(option.Key, settingRuleRequiresValues))
			return
		}
	case "EmailDomainWhitelist":
		// The restriction's own guard only runs when the switch is saved. Emptying
		// the list while the restriction stays on sends this key alone, and an
		// enabled restriction with no domains rejects every registration and
		// email-code sign-in. The invariant is held here, where every writer
		// passes, rather than trusting each form to send the pair in order.
		if common.EmailDomainRestrictionEnabled &&
			len(model.ParseEmailDomainWhitelist(option.Value.(string))) == 0 {
			httperr.Write(c, settingInvalid(option.Key, settingRuleLockedWhileEnabled))
			return
		}
	case "MinTopUpUSD":
		// Whole dollars from $1 up to the single-payment ceiling; anything else
		// would either refuse every top-up or admit one the ceiling refuses.
		minimum, parseErr := strconv.Atoi(strings.TrimSpace(option.Value.(string)))
		if parseErr != nil || minimum < 1 || minimum > setting.StripeMaxTopUp {
			httperr.Write(c, settingInvalid(option.Key, settingRuleIntegerRange, types.D{"min": 1, "max": setting.StripeMaxTopUp}))
			return
		}
	case "monitor_setting.auto_test_channel_minutes":
		// A zero interval is not "never": the hosted scheduler skips it, and the
		// legacy runner sleeps for zero and probes every channel in a loop.
		if minutes, parseErr := strconv.Atoi(strings.TrimSpace(option.Value.(string))); parseErr != nil || minutes < 1 {
			httperr.Write(c, settingInvalid(option.Key, settingRuleMinInteger, types.D{"limit": 1}))
			return
		}
	case "console_setting.announcements":
		normalizedValue, _, _, err := console_setting.NormalizeAnnouncementsJSONString(option.Value.(string))
		if err != nil {
			httperr.Write(c, err)
			return
		}
		option.Value = normalizedValue
		if err := console_setting.ValidateConsoleSettings(option.Value.(string), "Announcements"); err != nil {
			httperr.Write(c, err)
			return
		}
	case "console_setting.faq":
		if err := console_setting.ValidateConsoleSettings(option.Value.(string), "FAQ"); err != nil {
			httperr.Write(c, err)
			return
		}
	case "console_setting.uptime_kuma_groups":
		if err := console_setting.ValidateConsoleSettings(option.Value.(string), "UptimeKumaGroups"); err != nil {
			httperr.Write(c, err)
			return
		}
	}
	value := option.Value.(string)
	oldValue := model.GetOptionValue(option.Key)
	// model.UpdateOption fails only when a structured option (lanes and
	// similar JSON settings) cannot be parsed.
	if err := model.UpdateOption(option.Key, value); err != nil {
		httperr.Write(c, settingFormatInvalid(option.Key, err))
		return
	}
	if option.Key == "console_setting.announcements" {
		if err := model.SyncLegacyAnnouncements(value); err != nil {
			httperr.Write(c, err)
			return
		}
	}
	model.WriteOptionAuditLog(c, option.Key, oldValue, value)
	model.WriteAdminAuditLog(c, "settings.update", "option", option.Key, option.Key, gin.H{option.Key: oldValue}, gin.H{option.Key: value})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}
