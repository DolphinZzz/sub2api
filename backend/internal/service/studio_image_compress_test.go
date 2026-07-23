package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStudioImageRequestQuality(t *testing.T) {
	payload := json.RawMessage(`{"tools":[{"type":"image_generation","quality":"low"}]}`)
	require.Equal(t, "low", studioImageRequestQuality(payload))
	require.Empty(t, studioImageRequestQuality(json.RawMessage(`{"tools":[{"type":"web_search"}]}`)))
}

func TestCompressStudioImageToLimitPreservesFormat(t *testing.T) {
	source := noisyStudioTestPNG(t, 512, 512)
	for _, format := range []string{"jpeg", "png", "webp"} {
		t.Run(format, func(t *testing.T) {
			compressed, err := compressStudioImageToLimit(source, format, 96*1024)
			require.NoError(t, err)
			require.LessOrEqual(t, len(compressed), 96*1024)
			_, decodedFormat, err := image.Decode(bytes.NewReader(compressed))
			require.NoError(t, err)
			require.Equal(t, format, decodedFormat)
		})
	}
}

func TestStudioServicePersistOutputImageEnforcesLowQualityLimit(t *testing.T) {
	source := noisyStudioTestPNG(t, 1024, 1024)
	require.Greater(t, len(source), studioLowQualityMaxBytes)

	storage := &studioServiceStorageStub{}
	svc := NewStudioService(&studioServiceRepoStub{}, storage, nil, nil)
	rc := &StudioRequestContext{
		Request: &StudioRequest{
			ID:        "request-1",
			SessionID: "session-1",
			UserID:    7,
			Payload:   json.RawMessage(`{"tools":[{"type":"image_generation","quality":"low"}]}`),
		},
		AssistantMessage: &StudioMessage{ID: "assistant-1"},
	}

	asset, err := svc.PersistOutputImage(context.Background(), rc, base64.StdEncoding.EncodeToString(source), "png", "")
	require.NoError(t, err)
	require.LessOrEqual(t, len(storage.outputData), studioLowQualityMaxBytes)
	require.Equal(t, int64(len(storage.outputData)), asset.ByteSize)
	require.Equal(t, "png", storage.outputFormat)
}

func TestStudioServicePersistOutputImageLeavesOtherQualitiesUnchanged(t *testing.T) {
	source := noisyStudioTestPNG(t, 1024, 1024)
	storage := &studioServiceStorageStub{}
	svc := NewStudioService(&studioServiceRepoStub{}, storage, nil, nil)
	rc := &StudioRequestContext{
		Request: &StudioRequest{
			ID:        "request-1",
			SessionID: "session-1",
			UserID:    7,
			Payload:   json.RawMessage(`{"tools":[{"type":"image_generation","quality":"medium"}]}`),
		},
		AssistantMessage: &StudioMessage{ID: "assistant-1"},
	}

	_, err := svc.PersistOutputImage(context.Background(), rc, base64.StdEncoding.EncodeToString(source), "png", "")
	require.NoError(t, err)
	require.Equal(t, source, storage.outputData)
}

func TestStudioImageRequestQualitySupportsAsyncImagesPayload(t *testing.T) {
	require.Equal(t, "low", studioImageRequestQuality(json.RawMessage(`{"model":"gpt-image-2","quality":"low"}`)))
}

func noisyStudioTestPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	seed := uint32(1)
	for index := range img.Pix {
		seed = seed*1664525 + 1013904223
		img.Pix[index] = byte(seed >> 24)
	}
	var buf bytes.Buffer
	require.NoError(t, (&png.Encoder{CompressionLevel: png.NoCompression}).Encode(&buf, img))
	return buf.Bytes()
}
