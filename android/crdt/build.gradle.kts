// The CRDT engine: the AAR `make android-crdt` builds from mobile/crdt
// (Phase 12a) lands in libs/. This project publishes that file as its one
// artifact, so the app depends on `project(":crdt")` and gets the AAR's
// classes and native libraries as if it were a library module. (An
// Android library module cannot carry a local AAR; AGP refuses to bundle
// one.) The AAR is a build artifact, not committed: the realtime sync
// layer (Phase 12d) calls into it, so the app needs the AAR built first —
// CI does, and `make android-crdt` does locally.
val aar = file("libs/yana-crdt.aar")

configurations.create("default") {
    isCanBeConsumed = true
    isCanBeResolved = false
}

if (aar.exists()) {
    artifacts.add("default", aar)
}
