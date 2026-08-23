package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/megascan/open-grok/internal/types"
)

// ReferenceToVideo is the rust reference_to_video tool: generate a video from
// 2–7 reference images and a required prompt, saved to session/videos/<n>.mp4.
type ReferenceToVideo struct {
	HTTP         *http.Client
	BaseURL      string
	Bearer       string
	Model        string
	PollInterval time.Duration
}

func (ReferenceToVideo) Name() string { return "reference_to_video" }

func (ReferenceToVideo) Description() string {
	return "Generate a video from multiple reference images guided by a text prompt; returns the saved video's absolute path. When telling the user where it was saved, refer to it by its short session-relative path (e.g. `videos/1.mp4`) rather than the absolute path, so it renders as a clickable link that opens the video. Provide `images` with 2 to 7 image references and a required `prompt` describing the desired video. Use this tool when the user wants a video using multiple images as style/content references. Example: reference_to_video(prompt=\"blend these into a cinematic fashion shot with slow dolly movement\", images=[\"/Users/me/ref1.jpg\", \"/Users/me/ref2.jpg\"], aspect_ratio=\"16:9\", duration=6, resolution_name=\"480p\")"
}

func (ReferenceToVideo) Access() types.AccessKind { return types.AccessEdit }

func (ReferenceToVideo) Parameters() map[string]any {
	return objectSchema(map[string]any{
		"prompt": map[string]any{
			"type":        "string",
			"description": "Prompt to guide the video generation model. Describe the desired video.",
		},
		"images": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Reference images. Provide 2 to 7 entries; the images are used as style/content references for the generated video. Each entry may be an absolute filesystem path, HTTPS URL, or `data:image/...;base64,...` URL.",
		},
		"aspect_ratio": map[string]any{
			"type":        "string",
			"description": "Aspect ratio of the generated video, decide it based on the user's request. 1:1 for square (icons, profiles), 16:9 for wide (landscapes, cinematic), 9:16 for tall (phone wallpapers, stories), 3:2 for horizontal photos, 2:3 for vertical (portraits, posters).",
		},
		"duration": map[string]any{
			"type":        "integer",
			"description": "Duration of the generated video, either 6 or 10 seconds. Defaults to 6.",
		},
		"resolution_name": map[string]any{
			"type":        "string",
			"description": "Resolution of the generated video; only set this when the user asks for a specific resolution, either 480p or 720p. Defaults to 480p.",
		},
	}, []string{"prompt", "images", "aspect_ratio"})
}

func (t ReferenceToVideo) Execute(ctx context.Context, tc Context, args json.RawMessage) Result {
	m := unmarshal(args)
	prompt := strings.TrimSpace(str(m["prompt"]))
	if prompt == "" {
		return errResult("`prompt` must not be empty.")
	}
	images := stringList(m["images"])
	if len(images) < 2 {
		return errResult("`images` must contain at least two image references.")
	}
	if len(images) > maxRefImages {
		return errResult(fmt.Sprintf("`images` must contain at most %d image references.", maxRefImages))
	}
	duration, err := parseVideoDuration(m["duration"])
	if err != nil {
		return errResult(err.Error())
	}
	aspect, err := parseVideoAspect(m["aspect_ratio"])
	if err != nil {
		return errResult(err.Error())
	}
	resolution, err := parseVideoResolution(m["resolution_name"])
	if err != nil {
		return errResult(err.Error())
	}
	refs := make([]string, 0, len(images))
	for _, img := range images {
		u, err := resolveImageRef(img)
		if err != nil {
			return errResult(err.Error())
		}
		refs = append(refs, u)
	}
	model := strings.TrimSpace(t.Model)
	if model == "" {
		model = xaiVideoBaseModel
	}
	client := videoClient{
		HTTP:         t.HTTP,
		BaseURL:      t.BaseURL,
		Bearer:       t.Bearer,
		Model:        model,
		PollInterval: t.PollInterval,
	}
	return client.generate(ctx, tc, prompt, resolution, duration, &aspect, "", refs)
}
