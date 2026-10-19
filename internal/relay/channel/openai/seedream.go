package openai

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lazu-ai/lazu/internal/dto"
	relaycommon "github.com/lazu-ai/lazu/internal/relay/common"
	"github.com/lazu-ai/lazu/internal/service"
	"github.com/lazu-ai/lazu/internal/types"
	"github.com/lazu-ai/lazu/pkg/billing"
	"github.com/tidwall/sjson"
)

func seedreamUsage(body []byte, request *dto.ImageRequest) (*dto.Usage, error) {
	var response struct {
		Data []struct {
			URL   string          `json:"url"`
			B64   string          `json:"b64_json"`
			Size  string          `json:"size"`
			Error json.RawMessage `json:"error"`
		} `json:"data"`
		Usage dto.Usage `json:"usage"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	refs, err := request.ReferenceImages()
	if err != nil {
		return nil, err
	}
	inputImages := len(refs)
	if reported := response.Usage.InputImages; reported != nil {
		if *reported < 0 || *reported > len(refs) {
			return nil, fmt.Errorf("Seedream returned invalid reference usage")
		}
		inputImages = *reported
	}
	units := &billing.ImageUsage{InputImages: inputImages, Outputs: []billing.ImageOutput{}}
	for _, output := range response.Data {
		if len(output.Error) != 0 && string(output.Error) != "null" {
			continue
		}
		if output.URL == "" && output.B64 == "" {
			continue
		}
		pixels, err := dto.ImagePixels(output.Size)
		if err != nil {
			return nil, fmt.Errorf("Seedream output is missing valid pixel dimensions: %w", err)
		}
		units.Outputs = append(units.Outputs, billing.ImageOutput{Pixels: pixels, Layer: request.LayerDecomposition})
	}
	maximum := len(request.SeedreamReservation().Outputs)
	if len(units.Outputs) > maximum {
		return nil, fmt.Errorf("Seedream returned more images than requested")
	}
	if len(units.Outputs) == 0 {
		return nil, fmt.Errorf("Seedream returned no successful images")
	}
	usage := &response.Usage
	usage.ImageUsage = units
	count := len(units.Outputs)
	usage.GeneratedImages = &count
	usage.MarkProviderUsage()
	return usage, nil
}

func seedreamResponse(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response, body []byte, request *dto.ImageRequest) (*dto.Usage, *types.LazuError) {
	usage, err := seedreamUsage(body, request)
	if err != nil {
		return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponseBody, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
	}
	quote := service.BuildTextBillingQuote(c, info, usage, "")
	if len(quote.Quote.MissingPriceDimensions) > 0 {
		return nil, types.NewErrorWithStatusCode(fmt.Errorf("Seedream price dimensions are missing"), types.ErrorCodeBadResponseBody, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
	}
	service.AnnotateUsageCost(c, usage, quote.Quote)
	body, err = sjson.SetBytes(body, "usage.cost", service.QuoteCostUSD(quote.Quote))
	if err != nil {
		return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponseBody, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
	}
	body, err = sjson.SetBytes(body, "usage.generated_images", *usage.GeneratedImages)
	if err != nil {
		return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponseBody, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
	}
	service.AttachModelLogPayload(c, &service.ModelLogPayload{ResponseBody: append([]byte(nil), body...), Usage: *usage, StatusCode: resp.StatusCode})
	service.IOCopyBytesGracefully(c, resp, body)
	return usage, nil
}
