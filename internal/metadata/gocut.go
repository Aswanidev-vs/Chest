// Package metadata — GoCut project extractor (.gocut).
package metadata

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
)

// gocutMagic is the signature prefix of GoCut project files. Projects start
// with the "id" JSON key, so the prefix doubles as a content signature. The
// pattern tolerates pretty-printed projects ("{\n  \"id\": ...").
var gocutMagic = regexp.MustCompile(`^\s*\{\s*"id"\s*:`)

// gocutFile describes the JSON project structure of a .gocut file. Only the
// fields useful for organizing files are decoded; unknown fields are ignored
// by encoding/json.
type gocutFile struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	CreatedAt   string          `json:"createdAt"`
	UpdatedAt   string          `json:"updatedAt"`
	Duration    float64         `json:"duration"`
	AspectRatio string          `json:"aspectRatio"`
	Resolution  gocutResolution `json:"resolution"`
}

// gocutResolution is the embedded resolution block of a .gocut project.
type gocutResolution struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// gocutMatch reports whether a sample looks like a GoCut project: either the
// extension is .gocut or the content starts with the project signature.
func gocutMatch(sample Sample) bool {
	if sample.Extension == "gocut" {
		return true
	}
	head := sample.Head
	if len(head) > 512 {
		head = head[:512]
	}
	return gocutMagic.Match(head)
}

// extractGoCut parses a GoCut project file and reports its format, metadata
// fields and the embedded createdAt timestamp. Files that fail to parse (e.g.
// empty or truncated projects) still report the GoCut format without fields.
func extractGoCut(sample Sample) (Result, error) {
	res := Result{
		Format:   "GoCut",
		MIMEType: "application/x-gocut-project",
		Category: "Video",
	}

	var project gocutFile
	if err := json.Unmarshal(sampleBytes(sample), &project); err != nil && isJSONSyntaxError(err) {
		// Not valid JSON (empty/truncated file): report format only.
		return res, nil
	}

	addField(&res, "project_id", project.ID)
	addField(&res, "project_name", project.Name)
	addField(&res, "aspect_ratio", project.AspectRatio)
	if project.Resolution.Width > 0 && project.Resolution.Height > 0 {
		addField(&res, "resolution",
			strconv.Itoa(project.Resolution.Width)+"x"+strconv.Itoa(project.Resolution.Height))
	}
	if project.Duration > 0 {
		addField(&res, "duration_seconds", strconv.FormatFloat(project.Duration, 'f', -1, 64))
	}

	if t, ok := parseISO8601(project.CreatedAt); ok {
		setDate(&res, t, "gocut")
	}
	return res, nil
}

// isJSONSyntaxError reports whether err is a JSON syntax error (invalid
// structure) as opposed to a type mismatch, which we tolerate.
func isJSONSyntaxError(err error) bool {
	var syntaxErr *json.SyntaxError
	return errors.As(err, &syntaxErr)
}
