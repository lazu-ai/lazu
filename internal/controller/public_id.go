package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lazu-ai/lazu/internal/model"
	"github.com/lazu-ai/lazu/internal/types"
	"gorm.io/gorm"
)

// resolveIDParam turns a numeric or public ID into the row ID. A blank ID is
// missing_required_parameter on `id` and a failed lookup is internal. An ID
// that matches nothing stays gorm.ErrRecordNotFound, so a domain can answer
// with its own `<thing>_not_found` (see resolveChannelID); written as is, it
// is resource_not_found.
func resolveIDParam(table string, raw string) (int, error) {
	id, err := model.ResolvePublicIntID(table, raw)
	return id, publicIDError(table, raw, err)
}

func resolveIDRaw(table string, raw json.RawMessage) (int, error) {
	identifier, err := rawIdentifier(raw)
	if err != nil {
		return 0, err
	}
	return resolveIDParam(table, identifier)
}

func rawIdentifier(raw json.RawMessage) (string, error) {
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		return strings.TrimSpace(str), nil
	}
	var num json.Number
	if err := json.Unmarshal(raw, &num); err == nil {
		return num.String(), nil
	}
	var asFloat float64
	if err := json.Unmarshal(raw, &asFloat); err == nil {
		return strconv.FormatInt(int64(asFloat), 10), nil
	}
	return "", types.Err(types.ErrorCodeInvalidRequest, types.D{"field": "id"})
}

func publicIDError(table string, raw string, err error) error {
	switch {
	case err == nil:
		return nil
	case strings.TrimSpace(raw) == "":
		return types.Err(types.ErrorCodeMissingRequiredParameter, types.D{"field": "id"})
	case errors.Is(err, gorm.ErrRecordNotFound):
		return err
	default:
		return types.WrapErr(fmt.Errorf("resolve %s id: %w", table, err), types.ErrorCodeInternalError)
	}
}
