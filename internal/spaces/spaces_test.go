package spaces

import (
	"strings"
	"testing"
)

func TestParseValid(t *testing.T) {
	in := `name: household
members:
  - user: 01ARZ3NDEKTSV4RRFFQ69G5FAV
    role: owner
  - user: sam
    role: editor
`
	spec, err := Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if spec.Name != "household" || len(spec.Members) != 2 {
		t.Fatalf("spec: %+v", spec)
	}
	if spec.Members[1].User != "sam" || spec.Members[1].Role != RoleEditor {
		t.Fatalf("members: %+v", spec.Members)
	}
}

func TestParseRejects(t *testing.T) {
	cases := map[string]string{
		"bad role":      "members:\n- user: a\n  role: admin\n",
		"no user":       "members:\n- role: editor\n",
		"dup member":    "members:\n- user: a\n  role: viewer\n- user: A\n  role: editor\n",
		"unknown field": "name: x\nmembes: []\n",
		"not yaml doc":  "\tname: bad\n",
		"long name":     "name: " + strings.Repeat("n", 101) + "\n",
	}
	for name, in := range cases {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("%s: parse succeeded, want error", name)
		}
	}
}

func TestParseEmptyMembers(t *testing.T) {
	spec, err := Parse([]byte("name: solo\nmembers: []\n"))
	if err != nil || len(spec.Members) != 0 {
		t.Fatalf("spec=%+v err=%v", spec, err)
	}
}

func TestRenderRoundTrip(t *testing.T) {
	spec := Spec{Name: "household", Members: []Member{
		{User: "01ARZ3NDEKTSV4RRFFQ69G5FAV", Role: RoleOwner},
		{User: "sam", Role: RoleViewer},
	}}
	out := Render(spec)
	back, err := Parse([]byte(out))
	if err != nil {
		t.Fatalf("rendered file does not parse: %v\n%s", err, out)
	}
	if back.Name != spec.Name || len(back.Members) != 2 || back.Members[1] != spec.Members[1] {
		t.Fatalf("round trip changed the spec: %+v\n%s", back, out)
	}
}

func TestRenderQuotesHostileNames(t *testing.T) {
	out := string(Render(Spec{Members: []Member{{User: "a: b #c", Role: RoleEditor}}}))
	if !strings.Contains(out, `"a: b #c"`) {
		t.Fatalf("hostile user not quoted:\n%s", out)
	}
	if _, err := Parse([]byte(out)); err != nil {
		t.Fatalf("quoted output does not parse: %v", err)
	}
}

func TestIsFile(t *testing.T) {
	cases := map[string]bool{
		"home/.space.yml":  true,
		".space.yml":       false, // the root itself is not a space
		"a/b/.space.yml":   false, // nested; spaces are top-level only
		"home/space.yml":   false,
		"home/.space.yaml": false,
		"home/notes/x.md":  false,
		"a/.b/.space.yml":  false,
		".sync/.space.yml": false,
	}
	for rel, want := range cases {
		if got := IsFile(rel); got != want {
			t.Errorf("IsFile(%q)=%v want %v", rel, got, want)
		}
	}
}

func TestValidName(t *testing.T) {
	for _, bad := range []string{"", ".", "..", "a/b", ".hidden", " spaced"} {
		if ValidName(bad) {
			t.Errorf("ValidName(%q)=true", bad)
		}
	}
	for _, good := range []string{"home", "work-2", "Homelab_2026"} {
		if !ValidName(good) {
			t.Errorf("ValidName(%q)=false", good)
		}
	}
}

func TestRoleAtLeast(t *testing.T) {
	if !RoleAtLeast(RoleOwner, RoleEditor) || !RoleAtLeast(RoleEditor, RoleEditor) {
		t.Fatal("owner/editor ordering")
	}
	if RoleAtLeast(RoleViewer, RoleEditor) {
		t.Fatal("viewer must not satisfy editor")
	}
	if RoleAtLeast("", RoleViewer) {
		t.Fatal("no membership must not satisfy viewer")
	}
}
