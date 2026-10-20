package model

import (
	"strings"
	"sync"

	"github.com/lazu-ai/lazu/internal/common"
	"github.com/lazu-ai/lazu/internal/constant"
	"github.com/lazu-ai/lazu/internal/dto"
	"github.com/lazu-ai/lazu/internal/relay/channel/video"
)

var (
	modelVideoCapabilitiesLock sync.RWMutex
	modelVideoCapabilities     map[string]*dto.VideoCapabilities
)

func setModelVideoCapabilities(rows map[string]*dto.VideoCapabilities) {
	modelVideoCapabilitiesLock.Lock()
	defer modelVideoCapabilitiesLock.Unlock()
	modelVideoCapabilities = rows
}

// collectModelVideoCapabilities reads the operator rows of every video model
// in the catalog refresh, so request validation never queries per call.
func collectModelVideoCapabilities(metas map[string]*Model, endpointTypes map[string][]constant.EndpointType) map[string]*dto.VideoCapabilities {
	rows := make(map[string]*dto.VideoCapabilities)
	for name, meta := range metas {
		if meta == nil || !isVideoCatalogModel(name, endpointTypes[name]) {
			continue
		}
		if !meta.VideoCapabilities.IsEmpty() {
			copied := *meta.VideoCapabilities
			rows[name] = &copied
		}
	}
	return rows
}

func isVideoCatalogModel(name string, endpointTypes []constant.EndpointType) bool {
	for _, endpointType := range endpointTypes {
		if endpointType == constant.EndpointTypeVideoGeneration {
			return true
		}
	}
	return common.IsVideoGenerationModel(name)
}

// ModelVideoCapabilities is the row that validates a request for the model:
// the operator's own when written, else the family defaults. It never
// returns nil; an unknown model gets an empty row that constrains nothing.
func ModelVideoCapabilities(modelName string) *dto.VideoCapabilities {
	name := strings.TrimSpace(modelName)
	modelVideoCapabilitiesLock.RLock()
	row, ok := modelVideoCapabilities[name]
	modelVideoCapabilitiesLock.RUnlock()
	if ok && !row.IsEmpty() {
		copied := *row
		return &copied
	}
	return video.DefaultCapabilities(name)
}

// publicVideoCapabilities is the row the catalog publishes for a video model,
// nil for any other model.
func publicVideoCapabilities(modelName string, endpointTypes []constant.EndpointType) *dto.VideoCapabilities {
	if !isVideoCatalogModel(modelName, endpointTypes) {
		return nil
	}
	caps := ModelVideoCapabilities(modelName)
	if caps.IsEmpty() {
		return nil
	}
	return caps
}
