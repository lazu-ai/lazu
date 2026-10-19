package console_setting

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/lazu-ai/lazu/internal/types"
)

var (
	urlRegex       = regexp.MustCompile(`^https?://(?:(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)*[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?|(?:(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?))(?:\:[0-9]{1,5})?(?:/.*)?$`)
	dangerousChars = []string{"<script", "<iframe", "javascript:", "onload=", "onerror=", "onclick="}
	slugRegex      = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
)

const announcementRetentionDays = 60

// Option keys the console settings are stored under. Validation failures name
// them in details.field so the console can point at the field it came from.
const (
	FieldAnnouncements    = "console_setting.announcements"
	FieldFAQ              = "console_setting.faq"
	FieldUptimeKumaGroups = "console_setting.uptime_kuma_groups"
)

// Rules a setting value can break, sent as details.rule with setting_invalid.
const (
	RuleFormat        = "format"
	RuleRequired      = "required"
	RuleTooLong       = "too_long"
	RuleNotAllowed    = "not_allowed"
	RuleDuplicate     = "duplicate"
	RuleURL           = "url"
	RuleSlugChars     = "slug_chars"
	RuleUnsafeContent = "unsafe_content"
)

// invalidItem reports one list item's attribute. index is 1-based.
func invalidItem(field string, index int, item string, rule string, limit ...int) error {
	details := types.D{"field": field, "rule": rule, "index": index, "item": item}
	if len(limit) > 0 {
		details["limit"] = limit[0]
	}
	return types.Err(types.ErrorCodeSettingInvalid, details)
}

func tooMany(field string, limit int) error {
	return types.Err(types.ErrorCodeContentLimitExceeded, types.D{"field": field, "limit": limit})
}

func parseJSONArray(jsonStr string, field string) ([]map[string]interface{}, error) {
	var list []map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &list); err != nil {
		return nil, types.WrapErr(err, types.ErrorCodeSettingInvalid, types.D{"field": field, "rule": RuleFormat})
	}
	return list, nil
}

func validateURL(urlStr string, field string, index int) error {
	if !urlRegex.MatchString(urlStr) {
		return invalidItem(field, index, "url", RuleURL)
	}
	if _, err := url.Parse(urlStr); err != nil {
		return types.WrapErr(err, types.ErrorCodeSettingInvalid, types.D{"field": field, "rule": RuleURL, "index": index, "item": "url"})
	}
	return nil
}

func checkDangerousContent(content string, field string, index int, item string) error {
	lower := strings.ToLower(content)
	for _, d := range dangerousChars {
		if strings.Contains(lower, d) {
			return invalidItem(field, index, item, RuleUnsafeContent)
		}
	}
	return nil
}

func getJSONList(jsonStr string) []map[string]interface{} {
	if jsonStr == "" {
		return []map[string]interface{}{}
	}
	var list []map[string]interface{}
	json.Unmarshal([]byte(jsonStr), &list)
	return list
}

func ValidateConsoleSettings(settingsStr string, settingType string) error {
	if settingsStr == "" {
		return nil
	}

	switch settingType {
	case "Announcements":
		return validateAnnouncements(settingsStr)
	case "FAQ":
		return validateFAQ(settingsStr)
	case "UptimeKumaGroups":
		return validateUptimeKumaGroups(settingsStr)
	default:
		return fmt.Errorf("unknown console setting type %q", settingType)
	}
}

func validateAnnouncements(announcementsStr string) error {
	const field = FieldAnnouncements
	list, err := parseJSONArray(announcementsStr, field)
	if err != nil {
		return err
	}
	if len(list) > 100 {
		return tooMany(field, 100)
	}
	validTypes := map[string]bool{
		"default": true, "ongoing": true, "success": true, "warning": true, "error": true,
	}
	for i, ann := range list {
		content, ok := ann["content"].(string)
		if !ok || content == "" {
			return invalidItem(field, i+1, "content", RuleRequired)
		}
		publishDateAny, exists := ann["publishDate"]
		if !exists {
			return invalidItem(field, i+1, "publishDate", RuleRequired)
		}
		publishDateStr, ok := publishDateAny.(string)
		if !ok || publishDateStr == "" {
			return invalidItem(field, i+1, "publishDate", RuleRequired)
		}
		if _, err := time.Parse(time.RFC3339, publishDateStr); err != nil {
			return invalidItem(field, i+1, "publishDate", RuleFormat)
		}
		if t, exists := ann["type"]; exists {
			if typeStr, ok := t.(string); ok {
				if !validTypes[typeStr] {
					return invalidItem(field, i+1, "type", RuleNotAllowed)
				}
			}
		}
		if len(content) > 500 {
			return invalidItem(field, i+1, "content", RuleTooLong, 500)
		}
		if title, exists := ann["title"]; exists {
			if titleStr, ok := title.(string); ok && len(titleStr) > 80 {
				return invalidItem(field, i+1, "title", RuleTooLong, 80)
			}
		}
		if extra, exists := ann["extra"]; exists {
			if extraStr, ok := extra.(string); ok && len(extraStr) > 200 {
				return invalidItem(field, i+1, "extra", RuleTooLong, 200)
			}
		}
	}
	return nil
}

func validateFAQ(faqStr string) error {
	const field = FieldFAQ
	list, err := parseJSONArray(faqStr, field)
	if err != nil {
		return err
	}
	if len(list) > 100 {
		return tooMany(field, 100)
	}
	for i, faq := range list {
		question, ok := faq["question"].(string)
		if !ok || question == "" {
			return invalidItem(field, i+1, "question", RuleRequired)
		}
		answer, ok := faq["answer"].(string)
		if !ok || answer == "" {
			return invalidItem(field, i+1, "answer", RuleRequired)
		}
		if len(question) > 200 {
			return invalidItem(field, i+1, "question", RuleTooLong, 200)
		}
		if len(answer) > 1000 {
			return invalidItem(field, i+1, "answer", RuleTooLong, 1000)
		}
	}
	return nil
}

func getPublishTime(item map[string]interface{}) time.Time {
	if v, ok := item["publishDate"]; ok {
		if s, ok2 := v.(string); ok2 {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}

func normalizeAnnouncementsList(list []map[string]interface{}) []map[string]interface{} {
	cutoff := time.Now().AddDate(0, 0, -announcementRetentionDays)
	normalized := make([]map[string]interface{}, 0, len(list))

	for _, item := range list {
		publishTime := getPublishTime(item)
		if !publishTime.IsZero() && publishTime.Before(cutoff) {
			continue
		}
		normalized = append(normalized, item)
	}

	sort.SliceStable(normalized, func(i, j int) bool {
		return getPublishTime(normalized[i]).After(getPublishTime(normalized[j]))
	})

	return normalized
}

func NormalizeAnnouncementsJSONString(announcementsStr string) (string, []map[string]interface{}, bool, error) {
	if announcementsStr == "" {
		return "", []map[string]interface{}{}, false, nil
	}

	list, err := parseJSONArray(announcementsStr, FieldAnnouncements)
	if err != nil {
		return "", nil, false, err
	}

	normalized := normalizeAnnouncementsList(list)
	bytes, err := json.Marshal(normalized)
	if err != nil {
		return "", nil, false, err
	}

	normalizedJSON := string(bytes)
	if len(normalized) == 0 {
		normalizedJSON = ""
	}

	return normalizedJSON, normalized, normalizedJSON != announcementsStr, nil
}

func GetAnnouncements() []map[string]interface{} {
	return normalizeAnnouncementsList(getJSONList(GetConsoleSetting().Announcements))
}

func GetFAQ() []map[string]interface{} {
	return getJSONList(GetConsoleSetting().FAQ)
}

func validateUptimeKumaGroups(groupsStr string) error {
	const field = FieldUptimeKumaGroups
	groups, err := parseJSONArray(groupsStr, field)
	if err != nil {
		return err
	}

	if len(groups) > 20 {
		return tooMany(field, 20)
	}

	nameSet := make(map[string]bool)

	for i, group := range groups {
		categoryName, ok := group["categoryName"].(string)
		if !ok || categoryName == "" {
			return invalidItem(field, i+1, "categoryName", RuleRequired)
		}
		if nameSet[categoryName] {
			return invalidItem(field, i+1, "categoryName", RuleDuplicate)
		}
		nameSet[categoryName] = true
		urlStr, ok := group["url"].(string)
		if !ok || urlStr == "" {
			return invalidItem(field, i+1, "url", RuleRequired)
		}
		slug, ok := group["slug"].(string)
		if !ok || slug == "" {
			return invalidItem(field, i+1, "slug", RuleRequired)
		}
		description, ok := group["description"].(string)
		if !ok {
			description = ""
		}

		if err := validateURL(urlStr, field, i+1); err != nil {
			return err
		}

		if len(categoryName) > 50 {
			return invalidItem(field, i+1, "categoryName", RuleTooLong, 50)
		}
		if len(urlStr) > 500 {
			return invalidItem(field, i+1, "url", RuleTooLong, 500)
		}
		if len(slug) > 100 {
			return invalidItem(field, i+1, "slug", RuleTooLong, 100)
		}
		if len(description) > 200 {
			return invalidItem(field, i+1, "description", RuleTooLong, 200)
		}

		if !slugRegex.MatchString(slug) {
			return invalidItem(field, i+1, "slug", RuleSlugChars)
		}

		if err := checkDangerousContent(description, field, i+1, "description"); err != nil {
			return err
		}
		if err := checkDangerousContent(categoryName, field, i+1, "categoryName"); err != nil {
			return err
		}
	}
	return nil
}

func GetUptimeKumaGroups() []map[string]interface{} {
	return getJSONList(GetConsoleSetting().UptimeKumaGroups)
}
