package pager

import "strings"

// Canonical /imagine and /imagine-video wording from the Rust slash_commands
// crate so the model instruction cannot drift from the reference pager.

func imagineUsageMessage() string {
	return "Usage: /imagine <description>\nProvide a text description to generate an image."
}

func imagineVideoUsageMessage() string {
	return "Usage: /imagine-video <description>\nProvide a text description to generate a video."
}

func imagineInstruction(prompt string) string {
	return "Call the image_gen tool immediately, passing the user's prompt below " +
		"verbatim — do not rewrite, embellish, or expand it. " +
		"After the tool completes, briefly acknowledge and mention " +
		"where the image was saved.\n\n" +
		"Prompt: " + prompt
}

func imagineVideoInstruction(prompt string) string {
	return imagineVideoSkill() + "\n\nUser prompt: " + prompt
}

func imagineVideoSkill() string {
	// Inner identifiers are wrapped in backticks to match Rust IMAGINE_VIDEO_SKILL.
	bt := func(s string) string { return "`" + s + "`" }
	return strings.Join([]string{
		"# Imagine Video",
		"",
		"Video starts from an image — there is no text-to-video tool. " +
			"Default to " + bt("image_to_video") + "; use " + bt("reference_to_video") +
			" only when the user explicitly asks for it or a shot genuinely needs multiple reference images.",
		"",
		"## Default: single clip",
		"",
		"Unless the user asks for a long video, multiple scenes, or a multi-shot sequence, generate **one** video:",
		"",
		"1. Create a source image with " + bt("image_gen") + " that stages the first frame (composition, subject, lighting).",
		"2. Call " + bt("image_to_video") + " with that image and a short prompt describing the motion or camera move (1–2 sentences, present tense).",
		"3. After the tool completes, mention the saved file path so the user can find it.",
		"",
		"## Longer / multi-shot videos",
		"",
		"When the user requests a longer video, multiple scenes, or a narrative sequence:",
		"",
		"1. **Plan the story as shots** — break the idea into distinct shots, one beat each.",
		"2. **Favor frequent, short shots** — prefer more 6s clips over fewer long ones; more cuts keep it dynamic.",
		"3. **Create each shot's source image** with " + bt("image_gen") + " (or " + bt("image_edit") + " to combine references), keeping characters and settings consistent across shots.",
		"4. **Animate each shot with " + bt("image_to_video") + "** — the source image becomes frame 1.",
		"5. **Assemble with FFmpeg** using stream copy (" + bt("ffmpeg -f concat ... -c copy") + " — never re-encode). Keep every shot at the same resolution and frame rate so the concat works. After assembly, mention the final output path.",
		"",
		"## Shot guidance",
		"",
		"- **Prompt-craft:** one short, vivid moment in present tense with a clear camera movement, in 1–2 sentences.",
		"- **Minimal but interesting:** one clear subject, one simple motion or camera move per shot. Avoid complex multi-action animation; make the shot compelling through composition, lighting, and a strong moment.",
		"- **Complex source image?** Intricate frames (busy geometry, fine detail, heavy reflections) warp when animated. Keep the subject fixed and move only the camera (slow push-in, orbit, or parallax), or break into simpler shots. For new shots, generate a simpler, animation-friendly base image rather than animating a busy one.",
		"- **" + bt("image_to_video") + " animates from frame 1** — stage the first frame with " + bt("image_gen") + "/" + bt("image_edit") + " before animating.",
		"- **Aspect ratio:** set it on the source image (" + bt("image_gen") + " " + bt("aspect_ratio") + "); don't re-crop an existing video.",
		"- **Duration:** 6s or 10s only (prefer 6s); round to the nearest.",
		"- **Real people:** reference-first — drive the video from a verified reference image; never animate a named person without one.",
		"- Don't loop the same clip unless asked.",
	}, "\n")
}

func expandSubmit(text string) (display, wire string) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "/") {
		return text, text
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return text, text
	}
	name := strings.TrimPrefix(strings.ToLower(fields[0]), "/")
	arg := strings.TrimSpace(strings.TrimPrefix(trimmed, fields[0]))
	switch name {
	case "imagine":
		if arg == "" {
			return text, text
		}
		return "/imagine " + arg, imagineInstruction(arg)
	case "imagine-video":
		if arg == "" {
			return text, text
		}
		return "/imagine-video " + arg, imagineVideoInstruction(arg)
	default:
		return text, text
	}
}
