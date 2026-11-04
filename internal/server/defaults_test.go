package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultsFile(t *testing.T) {
	f := newAuthFixture(t)
	w := f.buildWorld(t)

	if code, body := doGet(t, f.ts, "/api/defaults", w.samHdr); code != 200 || body != nil {
		t.Fatalf("get before any put: %d %v", code, body)
	}
	d := Defaults{Space: "work", Folder: "/work/inbox/", DailyFolder: "work/journal"}
	if code, _ := doPost(t, f.ts, "PUT", "/api/defaults", w.samHdr, d); code != 403 {
		t.Fatalf("non-owner put %d", code)
	}
	if code, _ := doPost(t, f.ts, "PUT", "/api/defaults", w.ownerHdr, d); code != 200 {
		t.Fatalf("owner put %d", code)
	}
	raw, err := os.ReadFile(filepath.Join(f.root.Dir(), ".yana", "defaults.json"))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk Defaults
	if err := json.Unmarshal(raw, &onDisk); err != nil || onDisk != (Defaults{Space: "work", Folder: "work/inbox", DailyFolder: "work/journal"}) {
		t.Fatalf("file = %s (%v)", raw, err)
	}
	// sam is not in work: its names never reach sam.
	if code, body := doGet(t, f.ts, "/api/defaults", w.samHdr); code != 200 || body["space"] != "" || body["folder"] != "" || body["daily_folder"] != "" {
		t.Fatalf("get for a non-member: %d %v", code, body)
	}
	if code, _ := doPost(t, f.ts, "PUT", "/api/defaults", w.ownerHdr, Defaults{Space: "work", Folder: "work/inbox", DailySpace: "home", DailyFolder: "home/daily"}); code != 200 {
		t.Fatalf("owner put with home %d", code)
	}
	if code, body := doGet(t, f.ts, "/api/defaults", w.samHdr); code != 200 || body["folder"] != "" || body["daily_space"] != "home" || body["daily_folder"] != "home/daily" {
		t.Fatalf("get for a member of home: %d %v", code, body)
	}
	if code, body := doGet(t, f.ts, "/api/defaults", w.ownerHdr); code != 200 || body["folder"] != "work/inbox" {
		t.Fatalf("get for the owner: %d %v", code, body)
	}
	if code, _ := doGet(t, f.ts, "/api/defaults", ""); code != 401 {
		t.Fatalf("anonymous get %d", code)
	}
	if code, _ := doPost(t, f.ts, "PUT", "/api/defaults", w.ownerHdr, []string{"x"}); code != 400 {
		t.Fatalf("list body %d", code)
	}
	for _, bad := range []Defaults{{Folder: "work/../etc"}, {DailyFolder: ".yana/x"}, {Space: "a/b"}, {DailySpace: ".hidden"}} {
		if code, _ := doPost(t, f.ts, "PUT", "/api/defaults", w.ownerHdr, bad); code != 400 {
			t.Fatalf("%+v: %d", bad, code)
		}
	}
}
