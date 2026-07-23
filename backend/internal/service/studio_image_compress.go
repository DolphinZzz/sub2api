package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	stddraw "image/draw"
	"image/jpeg"
	"image/png"
	"math"

	nativewebp "github.com/HugoSmits86/nativewebp"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const studioLowQualityMaxBytes = 2 * 1024 * 1024
const studioMaxDecodedImagePixels = 50_000_000

var studioLowJPEGQualities = []int{65, 50, 35, 25}

func studioImageRequestQuality(payload json.RawMessage) string {
	var body struct {
		Quality string `json:"quality"`
		Tools   []struct {
			Type    string `json:"type"`
			Quality string `json:"quality"`
		} `json:"tools"`
	}
	if json.Unmarshal(payload, &body) != nil {
		return ""
	}
	if body.Quality != "" {
		return body.Quality
	}
	for _, tool := range body.Tools {
		if tool.Type == "image_generation" {
			return tool.Quality
		}
	}
	return ""
}

func compressStudioImageToLimit(data []byte, format string, maxBytes int) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, errors.New("image size limit must be positive")
	}
	if len(data) <= maxBytes {
		return data, nil
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > studioMaxDecodedImagePixels {
		return nil, errors.New("generated image is invalid")
	}
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil || source.Bounds().Empty() {
		return nil, errors.New("generated image is invalid")
	}

	current := source
	if format == "png" || format == "webp" {
		scale := math.Sqrt(float64(maxBytes)/float64(len(data))) * 0.92
		if scale < 0.95 {
			bounds := current.Bounds()
			scale = math.Max(0.25, scale)
			current = resizeStudioImage(
				current,
				max(1, int(math.Floor(float64(bounds.Dx())*scale))),
				max(1, int(math.Floor(float64(bounds.Dy())*scale))),
			)
		}
	}
	for attempt := 0; attempt < 8; attempt++ {
		candidate, err := encodeStudioImage(current, format, maxBytes)
		if err != nil {
			return nil, err
		}
		if len(candidate) <= maxBytes {
			return candidate, nil
		}

		bounds := current.Bounds()
		scale := math.Sqrt(float64(maxBytes)/float64(len(candidate))) * 0.92
		scale = math.Min(0.85, math.Max(0.25, scale))
		width := max(1, int(math.Floor(float64(bounds.Dx())*scale)))
		height := max(1, int(math.Floor(float64(bounds.Dy())*scale)))
		if width == bounds.Dx() && width > 1 {
			width--
		}
		if height == bounds.Dy() && height > 1 {
			height--
		}
		current = resizeStudioImage(current, width, height)
	}

	for current.Bounds().Dx() > 1 || current.Bounds().Dy() > 1 {
		bounds := current.Bounds()
		current = resizeStudioImage(current, max(1, bounds.Dx()/2), max(1, bounds.Dy()/2))
		candidate, err := encodeStudioImage(current, format, maxBytes)
		if err != nil {
			return nil, err
		}
		if len(candidate) <= maxBytes {
			return candidate, nil
		}
	}
	return nil, errors.New("generated image cannot be compressed below the size limit")
}

func encodeStudioImage(src image.Image, format string, maxBytes int) ([]byte, error) {
	switch format {
	case "jpeg":
		flattened := flattenStudioImage(src)
		var smallest []byte
		for _, quality := range studioLowJPEGQualities {
			var buf bytes.Buffer
			if err := jpeg.Encode(&buf, flattened, &jpeg.Options{Quality: quality}); err != nil {
				return nil, err
			}
			candidate := append([]byte(nil), buf.Bytes()...)
			if len(candidate) <= maxBytes {
				return candidate, nil
			}
			smallest = candidate
		}
		return smallest, nil
	case "png":
		var buf bytes.Buffer
		if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, src); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	case "webp":
		var buf bytes.Buffer
		if err := nativewebp.Encode(&buf, src, &nativewebp.Options{CompressionLevel: nativewebp.BestCompression}); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	default:
		return nil, errors.New("generated image format is unsupported")
	}
}

func resizeStudioImage(src image.Image, width, height int) image.Image {
	dst := image.NewNRGBA(image.Rect(0, 0, width, height))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), stddraw.Over, nil)
	return dst
}

func flattenStudioImage(src image.Image) image.Image {
	bounds := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	stddraw.Draw(dst, dst.Bounds(), &image.Uniform{C: color.White}, image.Point{}, stddraw.Src)
	stddraw.Draw(dst, dst.Bounds(), src, bounds.Min, stddraw.Over)
	return dst
}
