package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/megascan/open-grok/internal/types"
)

// ImageToVideo is the rust image_to_video tool: animate one source image via
// the xAI Video Generation API and save session/videos/<n>.mp4.
type ImageToVideo struct {
	HTTP         *http.Client
	BaseURL      string
	Bearer       string
	Model        string
	PollInterval time.Duration
}

func (ImageToVideo) Name() string { return "image_to_video" }

func (ImageToVideo) Description() string {
	return "Generate a video from a single source image; returns the saved video's absolute path. When telling the user where it was saved, refer to it by its short session-relative path (e.g. `videos/1.mp4`) rather than the absolute path, so it renders as a clickable link that opens the video. Provide `image` for the image to animate and optionally a `prompt` to guide the animation. Use this tool when the user provides an image and wants it animated, turned into a video, or used as the first frame. Example: image_to_video(image=\"/Users/me/photo.jpg\", prompt=\"gentle camera push-in with wind moving the hair\", duration=6, resolution_name=\"480p\")"
}

func (ImageToVideo) Access() types.AccessKind { return types.AccessEdit }

func (ImageToVideo) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"prompt": map[string]any{
			"type":        "string",
			"description": "Optional prompt to guide the video generation model. If omitted, a natural animation is applied automatically.",
		},
		"image": map[string]any{
			"type":        "string",
			"description": "Source image to animate. Provide an absolute filesystem path, HTTPS URL, or `data:image/...;base64,...` URL.",
		},
		"duration": map[string]any{
			"type":        "integer",
			"description": "Duration of the generated video, either 6 or 10 seconds. Default to 6 unless the user requests longer.",
		},
		"resolution_name": map[string]any{
			"type":        "string",
			"description": "Resolution of the generated video; only set this when the user asks for a specific resolution, either 480p or 720p. Defaults to 480p unless the user specifically requests higher quality.",
		},
	}, []string{"image"})
}

func (t ImageToVideo) Execute(ctx context.Context, tc Context, args json.RawMessage) Result {
	m := unmarshal(args)
	image := strings.TrimSpace(str(m["image"]))
	if image == "" {
		return errResult("image is required")
	}
	duration, err := parseVideoDuration(m["duration"])
	if err != nil {
		return errResult(err.Error())
	}
	resolution, err := parseVideoResolution(m["resolution_name"])
	if err != nil {
		return errResult(err.Error())
	}
	imageURL, err := resolveImageRef(image)
	if err != nil {
		return errResult(err.Error())
	}
	model := strings.TrimSpace(t.Model)
	if model == "" {
		model = xaiVideoQualityModel
	}
	client := videoClient{
		HTTP:         t.HTTP,
		BaseURL:      t.BaseURL,
		Bearer:       t.Bearer,
		Model:        model,
		PollInterval: t.PollInterval,
	}
	return client.generate(ctx, tc, str(m["prompt"]), resolution, duration, nil, imageURL, nil)
}
