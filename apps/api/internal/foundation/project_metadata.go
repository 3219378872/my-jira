package foundation

import (
	"net/url"
	"strings"

	"my-jira/apps/api/internal/platform/apperror"
)

const projectJSON = `to_jsonb(p)||jsonb_build_object('features','{"cycles":true,"modules":true,"pages":true,"views":true,"intake":true}'::jsonb||COALESCE(p.settings->'features','{}'::jsonb),'cover_image_url',COALESCE(p.settings->>'cover_image_url',''))`

func projectEventDTO(value map[string]any) map[string]any {
	result := map[string]any{}
	for _, key := range []string{"id", "workspace_id", "name", "identifier", "description", "network", "guest_can_view_all", "lead_id", "default_assignee_id", "timezone", "icon", "color", "features", "cover_image_url", "estimate_id", "archived_at", "created_at", "updated_at", "deleted_at"} {
		if field, exists := value[key]; exists {
			result[key] = field
		}
	}
	return result
}

func mergeJSONObjects(current, changes map[string]any) map[string]any {
	for key, value := range changes {
		if child, ok := value.(map[string]any); ok {
			previous, _ := current[key].(map[string]any)
			if previous == nil {
				previous = map[string]any{}
			}
			current[key] = mergeJSONObjects(previous, child)
		} else {
			current[key] = value
		}
	}
	return current
}

func validateProjectFeatures(value any) (map[string]any, error) {
	features, ok := value.(map[string]any)
	if !ok {
		return nil, apperror.Invalid("Features must be an object")
	}
	for name, enabled := range features {
		switch name {
		case "cycles", "modules", "pages", "views", "intake":
		default:
			return nil, apperror.Invalid("Unknown project feature: " + name)
		}
		if _, ok := enabled.(bool); !ok {
			return nil, apperror.Invalid("Project features must be true or false")
		}
	}
	return features, nil
}

func validateProjectCover(value any) (string, error) {
	if value == nil {
		return "", nil
	}
	text, ok := value.(string)
	if !ok || len(text) > 4096 {
		return "", apperror.Invalid("Invalid project cover URL")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}
	parsed, err := url.Parse(text)
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", apperror.Invalid("Project cover must be an HTTP or HTTPS image URL")
	}
	return text, nil
}

func validateProjectSettings(settings map[string]any) error {
	if value, present := settings["requirements_enabled"]; present && value != nil {
		if _, ok := value.(bool); !ok {
			return apperror.Invalid("requirements_enabled must be true, false or null")
		}
	}
	for _, key := range []string{"automation", "estimate_id", "estimate", "estimates", "estimate_settings", "guest_can_view_all"} {
		if _, exists := settings[key]; exists {
			return apperror.Invalid("Use the dedicated project settings endpoint for " + key)
		}
	}
	if features, ok := settings["features"]; ok {
		if _, err := validateProjectFeatures(features); err != nil {
			return err
		}
	}
	if cover, ok := settings["cover_image_url"]; ok {
		text, err := validateProjectCover(cover)
		if err != nil {
			return err
		}
		settings["cover_image_url"] = text
	}
	return nil
}
