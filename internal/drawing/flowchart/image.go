package flowchart

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"time"
)

// ErrImageType is returned for anything but png, jpeg or gif.
var ErrImageType = errors.New("unsupported image type: use png, jpeg or gif")

// File is an image file a drawing carries.
type File struct {
	ID, MimeType string
	Data         []byte
}

// Files returns the decoded data URLs of the files live image elements use,
// in z-order, each once.
func Files(sceneJSON []byte) ([]File, error) {
	s, err := ParseScene(sceneJSON)
	if err != nil {
		return nil, err
	}
	var out []File
	seen := map[string]bool{}
	for _, e := range s.Live() {
		id := e.str("fileId")
		raw, ok := s.Files[id]
		if e.Type() != "image" || id == "" || seen[id] || !ok {
			continue
		}
		var f struct {
			MimeType string `json:"mimeType"`
			DataURL  string `json:"dataURL"`
		}
		if err := json.Unmarshal(raw, &f); err != nil {
			return nil, fmt.Errorf("file %s: %w", id, err)
		}
		head, b64, ok := strings.Cut(f.DataURL, ",")
		if !ok || !strings.HasSuffix(head, ";base64") {
			return nil, fmt.Errorf("file %s: not a base64 data URL", id)
		}
		data, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("file %s: %w", id, err)
		}
		seen[id] = true
		out = append(out, File{ID: id, MimeType: f.MimeType, Data: data})
	}
	return out, nil
}

// AddImage adds data (png, jpeg or gif) as an image element. near is a node
// id ("" = none); width 0 means natural width capped at 400. The image goes
// right of near, else below all content, clear of other elements. data and
// sceneJSON are not modified; on error nothing is returned.
func AddImage(sceneJSON []byte, data []byte, near string, width float64, now time.Time) ([]byte, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width == 0 || cfg.Height == 0 {
		return nil, ErrImageType
	}
	v, err := Read(sceneJSON)
	if err != nil {
		return nil, err
	}
	s, err := ParseScene(sceneJSON)
	if err != nil {
		return nil, err
	}
	var nearBox *Box
	if near != "" {
		for _, it := range v.Items {
			if it.ID == near && len(it.Elements) > 0 {
				if e := s.ByID(it.Elements[0]); e != nil {
					b := box(e)
					nearBox = &b
				}
			}
		}
		if nearBox == nil {
			return nil, &UnknownIDsError{[]string{near}}
		}
	}
	w := width
	if w <= 0 {
		w = min(float64(cfg.Width), 400)
	}
	h := w * float64(cfg.Height) / float64(cfg.Width)

	var obstacles []Box
	for _, e := range s.Live() {
		obstacles = append(obstacles, bbox(e))
	}
	var x, y float64
	found := false
	if nearBox != nil {
		x, y, found = freeSpot(obstacles, nearBox.X+nearBox.W+rankGap, nearBox.Y+nearBox.H/2-h/2, Box{W: w, H: h}, true)
	}
	if !found {
		x, y = pastAll(obstacles, false)
	}

	sum := sha1.Sum(data)
	id := hex.EncodeToString(sum[:])
	mime := "image/" + format
	ms := float64(now.UnixMilli())
	file, _ := json.Marshal(map[string]any{
		"mimeType": mime, "id": id, "created": ms, "lastRetrieved": ms,
		"dataURL": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data),
	})
	s.Files[id] = file

	e := base("image", x, y, w, h, now)
	e["strokeColor"] = "transparent" // what Excalidraw gives images
	e["fileId"], e["status"], e["scale"], e["crop"] = id, "saved", []any{1.0, 1.0}, nil
	if err := s.Append(e); err != nil {
		return nil, err
	}
	return s.JSON()
}
