package flowchart

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"testing"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func images(s *Scene) []El {
	var out []El
	for _, e := range s.Live() {
		if e.Type() == "image" {
			out = append(out, e)
		}
	}
	return out
}

func TestAddImage(t *testing.T) {
	data := pngBytes(t, 2, 1)
	orig := append([]byte(nil), data...)
	scene := newScene(t, "flowchart LR\n A-->B")
	out, err := AddImage(scene, data, "", 0, later)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, orig) {
		t.Fatal("input mutated")
	}
	ims := images(mustScene(t, out))
	if len(ims) != 1 || ims[0].W() != 2 || ims[0].H() != 1 {
		t.Fatalf("image elements: %v", ims)
	}
	if ims[0]["status"] != "saved" || ims[0]["crop"] != nil || ims[0]["strokeColor"] == "" {
		t.Fatalf("fields: %v", ims[0])
	}
	fs, err := Files(out)
	if err != nil || len(fs) != 1 || fs[0].MimeType != "image/png" || !bytes.Equal(fs[0].Data, data) {
		t.Fatalf("Files: %v %v", fs, err)
	}
	if fs[0].ID != ims[0].str("fileId") {
		t.Fatal("file id mismatch")
	}
}

func TestAddImageWidthAndCap(t *testing.T) {
	out, err := AddImage(newScene(t, "flowchart LR\n A-->B"), pngBytes(t, 2, 1), "", 300, later)
	if err != nil {
		t.Fatal(err)
	}
	if e := images(mustScene(t, out))[0]; e.W() != 300 || e.H() != 150 {
		t.Fatalf("got %vx%v", e.W(), e.H())
	}
	out, _ = AddImage(newScene(t, "flowchart LR\n A-->B"), pngBytes(t, 1000, 500), "", 0, later)
	if e := images(mustScene(t, out))[0]; e.W() != 400 || e.H() != 200 {
		t.Fatalf("cap: got %vx%v", e.W(), e.H())
	}
}

func TestAddImageSameTwiceSharesFile(t *testing.T) {
	data := pngBytes(t, 2, 1)
	out, _ := AddImage(newScene(t, "flowchart LR\n A-->B"), data, "", 0, later)
	out, err := AddImage(out, data, "", 0, later)
	if err != nil {
		t.Fatal(err)
	}
	s := mustScene(t, out)
	if len(images(s)) != 2 || len(s.Files) != 1 {
		t.Fatalf("%d images, %d files", len(images(s)), len(s.Files))
	}
	if fs, _ := Files(out); len(fs) != 1 {
		t.Fatalf("Files: %d", len(fs))
	}
}

func TestAddImageErrors(t *testing.T) {
	scene := newScene(t, "flowchart LR\n A-->B")
	webp := append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), make([]byte, 20)...)
	if _, err := AddImage(scene, webp, "", 0, later); !errors.Is(err, ErrImageType) {
		t.Fatalf("webp: %v", err)
	}
	_, err := AddImage(scene, pngBytes(t, 2, 1), "nope", 0, later)
	var u *UnknownIDsError
	if !errors.As(err, &u) {
		t.Fatalf("near: %v", err)
	}
}

func TestAddImageNearNoOverlap(t *testing.T) {
	scene := newScene(t, "flowchart LR\n A-->B")
	out, err := AddImage(scene, pngBytes(t, 100, 50), "A", 0, later)
	if err != nil {
		t.Fatal(err)
	}
	s := mustScene(t, out)
	a, im := yel(s, "A"), images(s)[0]
	if im.X() != a.X()+a.W()+80 {
		t.Fatalf("not right of A: %v vs %v", im.X(), a.X()+a.W())
	}
	for _, e := range s.Live() {
		if e.ID() != im.ID() && hits(box(im), bbox(e)) {
			t.Fatalf("image overlaps %s", e.ID())
		}
	}
}

func TestAddImageBelowAll(t *testing.T) {
	scene := newScene(t, "flowchart LR\n A-->B")
	before := mustScene(t, scene)
	var bs []Box
	for _, e := range before.Live() {
		bs = append(bs, bbox(e))
	}
	u := union(bs)
	out, _ := AddImage(scene, pngBytes(t, 100, 50), "", 0, later)
	im := images(mustScene(t, out))[0]
	if im.X() != u.X || im.Y() != u.Y+u.H+80 {
		t.Fatalf("got %v,%v want %v,%v", im.X(), im.Y(), u.X, u.Y+u.H+80)
	}
}

func TestFilesOnlyLiveImages(t *testing.T) {
	out, _ := AddImage(newScene(t, "flowchart LR\n A-->B"), pngBytes(t, 2, 1), "", 0, later)
	out = editScene(t, out, func(s *Scene) {
		for _, e := range s.Elements {
			if e.Type() == "image" {
				e["isDeleted"] = true
			}
		}
	})
	if fs, err := Files(out); err != nil || len(fs) != 0 {
		t.Fatalf("%v %v", fs, err)
	}
}
