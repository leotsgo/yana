package drawing

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// el is one element as Excalidraw writes it, enough for the merge.
func el(id string, version, nonce int, deleted bool, index string) string {
	return fmt.Sprintf(`{"id":%q,"type":"rectangle","version":%d,"versionNonce":%d,"isDeleted":%t,"index":%q}`, id, version, nonce, deleted, index)
}

func scene(els ...string) []byte {
	return []byte(`{"type":"excalidraw","version":2,"elements":[` + strings.Join(els, ",") + `],"appState":{"viewBackgroundColor":"#fff"},"files":{}}`)
}

// got maps id → "version/nonce/deleted" of the merged scene, and fails on
// a duplicated id.
func got(t *testing.T, merged []byte) (map[string]string, []string) {
	t.Helper()
	var s struct {
		Elements []stamp
	}
	if err := json.Unmarshal(merged, &s); err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	var order []string
	for _, e := range s.Elements {
		if _, dup := m[e.ID]; dup {
			t.Fatalf("element %s twice in %s", e.ID, merged)
		}
		m[e.ID] = fmt.Sprintf("%v/%v/%v", e.Version, e.VersionNonce, e.IsDeleted)
		order = append(order, e.ID)
	}
	return m, order
}

func TestMerge(t *testing.T) {
	for _, c := range []struct {
		name      string
		disk, in  []byte
		want      map[string]string
		changed   bool
		wantOrder string
	}{
		{
			name: "newer version on disk wins",
			disk: scene(el("a", 3, 9, false, "a0")),
			in:   scene(el("a", 2, 1, false, "a0")),
			want: map[string]string{"a": "3/9/false"}, changed: true,
		},
		{
			name: "newer version incoming wins",
			disk: scene(el("a", 2, 1, false, "a0")),
			in:   scene(el("a", 3, 9, false, "a0")),
			want: map[string]string{"a": "3/9/false"},
		},
		{
			name: "a newer tombstone wins",
			disk: scene(el("a", 2, 1, false, "a0")),
			in:   scene(el("a", 3, 5, true, "a0")),
			want: map[string]string{"a": "3/5/true"},
		},
		{
			name: "a newer tombstone on disk wins too",
			disk: scene(el("a", 4, 5, true, "a0")),
			in:   scene(el("a", 3, 1, false, "a0")),
			want: map[string]string{"a": "4/5/true"}, changed: true,
		},
		{
			name: "an older tombstone loses",
			disk: scene(el("a", 2, 1, true, "a0")),
			in:   scene(el("a", 3, 5, false, "a0")),
			want: map[string]string{"a": "3/5/false"},
		},
		{
			name: "equal versions: the lower nonce wins (disk)",
			disk: scene(el("a", 2, 10, false, "a0")),
			in:   scene(el("a", 2, 20, false, "a0")),
			want: map[string]string{"a": "2/10/false"}, changed: true,
		},
		{
			name: "equal versions: the lower nonce wins (incoming)",
			disk: scene(el("a", 2, 20, false, "a0")),
			in:   scene(el("a", 2, 10, false, "a0")),
			want: map[string]string{"a": "2/10/false"},
		},
		{
			name: "elements only on one side are kept, in index order",
			disk: scene(el("a", 1, 1, false, "a0"), el("d", 1, 1, false, "a2")),
			in:   scene(el("a", 1, 1, false, "a0"), el("i", 1, 1, false, "a1")),
			want: map[string]string{"a": "1/1/false", "d": "1/1/false", "i": "1/1/false"}, changed: true,
			wantOrder: "a,i,d",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			merged, changed, err := Merge(c.disk, c.in)
			if err != nil {
				t.Fatal(err)
			}
			if changed != c.changed {
				t.Errorf("changed = %v, want %v", changed, c.changed)
			}
			if !changed && string(merged) != string(c.in) {
				t.Errorf("unchanged merge rewrote the scene: %s", merged)
			}
			if err := Validate(merged); err != nil {
				t.Fatalf("merged scene is not a scene: %v", err)
			}
			m, order := got(t, merged)
			if fmt.Sprint(m) != fmt.Sprint(c.want) {
				t.Errorf("merged = %v, want %v", m, c.want)
			}
			if c.wantOrder != "" && strings.Join(order, ",") != c.wantOrder {
				t.Errorf("order = %v, want %s", order, c.wantOrder)
			}
		})
	}
}

// The incoming scene's appState is kept; files are the union, limited to
// those a live element uses.
func TestMergeFiles(t *testing.T) {
	img := func(id, file string, deleted bool) string {
		return fmt.Sprintf(`{"id":%q,"type":"image","version":1,"versionNonce":1,"isDeleted":%t,"index":"a%s","fileId":%q}`, id, deleted, id, file)
	}
	disk := []byte(`{"type":"excalidraw","elements":[` + img("1", "f1", false) + `,` + img("2", "f2", true) + `],"appState":{"viewBackgroundColor":"#000"},"files":{"f1":{"id":"f1","dataURL":"data:x"},"f2":{"id":"f2","dataURL":"data:y"}}}`)
	in := []byte(`{"type":"excalidraw","elements":[` + img("3", "f3", false) + `],"appState":{"viewBackgroundColor":"#fff"},"files":{"f3":{"id":"f3","dataURL":"data:z"}}}`)
	merged, changed, err := Merge(disk, in)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	var s struct {
		AppState map[string]any            `json:"appState"`
		Files    map[string]map[string]any `json:"files"`
	}
	if err := json.Unmarshal(merged, &s); err != nil {
		t.Fatal(err)
	}
	if s.AppState["viewBackgroundColor"] != "#fff" {
		t.Errorf("appState = %v", s.AppState)
	}
	if len(s.Files) != 2 || s.Files["f1"] == nil || s.Files["f3"] == nil {
		t.Errorf("files = %v", s.Files)
	}
}

func TestMergeRefusesIncoming(t *testing.T) {
	for _, in := range []string{
		`{"type":"excalidraw","elements":{}}`,
		`{"type":"excalidraw","elements":[{"id":"a","version":"x"}]}`,
		`{"type":"excalidraw","elements":[],"files":[]}`,
	} {
		if _, _, err := Merge(scene(), []byte(in)); !errors.Is(err, ErrIncoming) {
			t.Errorf("Merge(%s) err = %v, want ErrIncoming", in, err)
		}
	}
	if _, _, err := Merge([]byte(`{"elements":7}`), scene()); err == nil || errors.Is(err, ErrIncoming) {
		t.Errorf("bad disk scene: err = %v", err)
	}
}
