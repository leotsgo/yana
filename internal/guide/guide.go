// Package guide holds the starter notes that teach the app by using it:
// a "Start here" note that demonstrates links, pictures, tasks and tags
// with real content, and a second note for its resolved wikilink to
// land on. They are ordinary files; the server writes them once into an
// empty tree, or into a space on request, and never touches them again.
package guide

import (
	"embed"
	"path"
)

//go:embed content
var content embed.FS

// NoteName is the file name of the starter note.
const NoteName = "Start here.md"

// LinkedName is the companion note the starter note's resolved wikilink
// lands on. It sits beside the starter note, as does the _assets
// directory, so seeding a space adds no directory that could read as a
// space of its own at the tree root.
const LinkedName = "A linked note.md"

// File is one file to write, at a path relative to the space root.
type File struct {
	Rel  string
	Data []byte
}

// Files returns the starter note and everything it references, in the
// order they should be written. The starter note comes first.
func Files() []File {
	read := func(name string) []byte {
		b, err := content.ReadFile(path.Join("content", name))
		if err != nil {
			panic("guide: missing embedded file " + name)
		}
		return b
	}
	return []File{
		{Rel: NoteName, Data: read("start-here.md")},
		{Rel: LinkedName, Data: read("linked-note.md")},
		{Rel: "_assets/yana.png", Data: read("yana.png")},
	}
}
