package com.collinpendleton.yana.data

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

/** The server's JSON: unknown fields are ignored so newer servers stay readable. */
val YanaJson = Json {
    ignoreUnknownKeys = true
    explicitNulls = false
    coerceInputValues = true
}

@Serializable
data class AuthState(
    @SerialName("setup_required") val setupRequired: Boolean = false,
    val user: User? = null,
)

@Serializable
data class User(
    val id: String,
    val username: String,
    @SerialName("is_owner") val isOwner: Boolean = false,
)

@Serializable
data class Tokens(
    @SerialName("access_token") val access: String,
    @SerialName("access_expires_at") val accessExpiresAt: String,
    @SerialName("refresh_token") val refresh: String,
    @SerialName("session_id") val sessionId: String,
    @SerialName("refresh_expires_at") val refreshExpiresAt: String = "",
)

@Serializable
data class Credentials(val username: String, val password: String, val label: String)

@Serializable
data class SignInResponse(val user: User, val tokens: Tokens)

@Serializable
data class RefreshRequest(@SerialName("refresh_token") val refreshToken: String)

@Serializable
data class RefreshResponse(val tokens: Tokens)

@Serializable
data class ApiErrorBody(
    val error: String? = null,
    @SerialName("setup_required") val setupRequired: Boolean = false,
)

@Serializable
data class SpacesResponse(val spaces: List<Space> = emptyList())

/** One space: [name] is its directory ("" for notes loose in the root). */
@Serializable
data class Space(
    val name: String,
    val label: String = "",
    val notes: Int = 0,
) {
    val displayName: String get() = if (name.isEmpty()) "/" else label.ifEmpty { name }
}

@Serializable
data class TreeResponse(val spaces: List<SpaceTree> = emptyList())

@Serializable
data class SpaceTree(
    val name: String,
    val notes: Int = 0,
    val children: List<TreeNode> = emptyList(),
)

@Serializable
data class TreeNode(
    val type: String,
    val name: String,
    val path: String,
    val id: String? = null,
    val title: String? = null,
    val kind: String? = null,
    val tags: List<String> = emptyList(),
    val public: Boolean = false,
    val conflict: Boolean = false,
    val children: List<TreeNode> = emptyList(),
) {
    val isDir: Boolean get() = type == "dir"
    val label: String get() = if (isDir) name else title?.ifEmpty { null } ?: name
}

/** One [[wikilink]] of a note, as the server resolved it at index time. */
@Serializable
data class ServerLink(
    @SerialName("raw_target") val rawTarget: String = "",
    @SerialName("to_id") val toId: String? = null,
    val resolved: Boolean = false,
)

/** A task tick: one checkbox of one note, set open or done. */
@Serializable
data class TaskTickRequest(val note: String, val line: Int, val done: Boolean)

/**
 * The note a task sits in, without the payload-heavy fields — the same
 * trimmed shape the web's tasks page carries.
 */
@Serializable
data class TaskNote(
    val id: String,
    val space: String = "",
    val path: String = "",
    val title: String = "",
    val kind: String = "md",
)

/**
 * One `- [ ]` line of a note, as the tasks page lists it. [text] is the
 * line's markdown rendered to inline HTML at scan time; [line] is the
 * body line the rendered checkbox's data-line carries.
 */
@Serializable
data class TaskRow(
    val note: TaskNote,
    val line: Int,
    val indent: Int = 0,
    val text: String = "",
    val done: Boolean = false,
    @SerialName("done_at") val doneAt: String? = null,
    val heading: String = "",
)

@Serializable
data class TasksResponse(val tasks: List<TaskRow> = emptyList())

@Serializable
data class TaskCountResponse(val count: Int = 0)

/** One #tag and how many notes carry it, for the tag filter. */
@Serializable
data class TagCount(val tag: String, val count: Int = 0)

@Serializable
data class TagsResponse(val tags: List<TagCount> = emptyList())

/** The notes carrying one tag, in path order: GET /api/tags/{tag}. */
@Serializable
data class TagNotesResponse(val tag: String = "", val notes: List<NoteMeta> = emptyList())

/**
 * One note linking to another, as the backlinks endpoint answers it:
 * the linking note, the [[target]] exactly as written, and the line the
 * link sits on.
 */
@Serializable
data class Backlink(
    val note: NoteMeta = NoteMeta(id = "", path = ""),
    @SerialName("raw_target") val rawTarget: String = "",
    val context: String = "",
)

@Serializable
data class BacklinksResponse(val backlinks: List<Backlink> = emptyList())

@Serializable
data class Note(
    val id: String,
    val space: String = "",
    val path: String,
    val title: String = "",
    val preview: String = "",
    val kind: String = "md",
    val size: Long = 0,
    val created: String = "",
    @SerialName("updated_at") val updatedAt: String = "",
    val tags: List<String> = emptyList(),
    val role: String = "",
    val markdown: String? = null,
    val source: String? = null,
    val public: Boolean = false,
    val trusted: Boolean = false,
    @SerialName("content_hash") val contentHash: String = "",
    /** The note's directory, for resolving relative images and create paths. */
    val base: String = "",
    val links: List<ServerLink> = emptyList(),
    /** How many conflict copies point at this note, for the banner above its body. */
    @SerialName("conflict_count") val conflictCount: Int = 0,
)

/** The content-origin URL a rendered HTML note loads, signed for a few minutes. */
@Serializable
data class NoteView(
    val url: String,
    @SerialName("expires_at") val expiresAt: String = "",
)

@Serializable
data class SaveSourceRequest(
    val source: String,
    @SerialName("base_hash") val baseHash: String,
)

/** A source save: last-write-wins, with the diverged disk version parked at [conflictCopy] when there was one. */
@Serializable
data class SaveSourceResponse(
    val ok: Boolean = false,
    val path: String? = null,
    val hash: String? = null,
    @SerialName("conflict_copy") val conflictCopy: String? = null,
)

/**
 * One uploaded asset's answer: the path that was written (the server
 * picks a free name rather than overwriting), its size, and the URL it
 * serves at.
 */
@Serializable
data class UploadResponse(
    val path: String = "",
    val name: String = "",
    val size: Long = 0,
    val url: String = "",
)

@Serializable
data class SessionsResponse(val sessions: List<SessionInfo> = emptyList())

/**
 * One note's metadata: the full row the search and list endpoints
 * return, tags included. This is what the offline replica caches for
 * every visible note.
 */
@Serializable
data class NoteMeta(
    val id: String,
    val space: String = "",
    val path: String,
    val title: String = "",
    val preview: String = "",
    val kind: String = "md",
    @SerialName("content_hash") val contentHash: String = "",
    val size: Long = 0,
    val mtime: String = "",
    val created: String = "",
    @SerialName("updated_at") val updatedAt: String = "",
    val order: Int? = null,
    val trusted: Boolean = false,
    @SerialName("conflict_of") val conflictOf: String? = null,
    val tags: List<String> = emptyList(),
)

/** The flat note list the replica syncs from. */
@Serializable
data class NotesResponse(val notes: List<NoteMeta> = emptyList())

/** One full-text result, the same shape online and offline. */
@Serializable
data class SearchHit(
    val note: NoteMeta,
    val snippet: String = "",
    val rank: Double = 0.0,
)

@Serializable
data class SearchResponse(
    val mode: String = "fts",
    val hits: List<SearchHit> = emptyList(),
)

@Serializable
data class CreateNoteRequest(val path: String, val content: String)

@Serializable
data class CreateNoteResponse(val id: String = "", val path: String = "")

/** Today's note, made or found: POST /api/notes/daily. */
@Serializable
data class DailyNoteRequest(val space: String = "", val date: String)

@Serializable
data class DailyNoteResponse(val id: String = "", val path: String = "", val created: Boolean = false)

/** The server's face: only the daily-note pattern capture reads. */
@Serializable
data class ServerStatus(val daily: DailyStatus = DailyStatus())

@Serializable
data class DailyStatus(val pattern: String = "", val template: String = "")

@Serializable
data class MoveRequest(val path: String)

@Serializable
data class SessionInfo(
    val id: String,
    val label: String = "",
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("last_used_at") val lastUsedAt: String = "",
    /** When the session lapses; an empty string when the server did not say. */
    @SerialName("expires_at") val expiresAt: String = "",
    /** Set the moment a session is revoked; null while it lives. */
    @SerialName("revoked_at") val revokedAt: String? = null,
    val current: Boolean = false,
)

/** One account row of the owner's People list. */
@Serializable
data class UserInfo(
    val id: String,
    val username: String = "",
    @SerialName("is_owner") val isOwner: Boolean = false,
    @SerialName("created_at") val createdAt: String = "",
)

@Serializable
data class UsersResponse(val users: List<UserInfo> = emptyList())

@Serializable
data class CreateUserRequest(val username: String, val password: String)

@Serializable
data class PasswordRequest(val password: String)

/** One member of a space, as the owner's view of it carries. */
@Serializable
data class SpaceMemberRow(
    val user: String,
    val role: String = "",
    val id: String? = null,
    val username: String? = null,
) {
    /** The name a row shows: the account's username when the reference resolved. */
    val displayName: String get() = username?.ifEmpty { null } ?: user
}

/** One space as `GET /api/spaces/{space}` answers: the caller's role, and the member list for an owner. */
@Serializable
data class SpaceDetail(
    val name: String = "",
    val label: String = "",
    val role: String = "",
    val members: List<SpaceMemberRow> = emptyList(),
)

/** A member as `PATCH /api/spaces/{space}` writes it: a username or id, and one of the three roles. */
@Serializable
data class MemberSpec(val user: String, val role: String)

/** The whole of a space's sharing at once: the label and the member list, replaced. */
@Serializable
data class UpdateSpaceRequest(val name: String, val members: List<MemberSpec>)

@Serializable
data class CreateSpaceRequest(val name: String)

@Serializable
data class CreateSpaceResponse(val name: String = "")

/** Asks the server for the Start here note, making it in [space] when absent. */
@Serializable
data class GuideRequest(val space: String)

/** The guide answer: [id] is absent when the note was written but not yet indexed. */
@Serializable
data class GuideResponse(
    val id: String? = null,
    val path: String = "",
    val created: Boolean = false,
)

/** One revision of a note, as the git history holds it, renames followed. */
@Serializable
data class HistoryEntry(
    val hash: String,
    val name: String = "",
    val email: String = "",
    val date: String = "",
    val subject: String = "",
    /** Who the author is: person, agent, or filesystem. */
    val kind: String = "person",
    /** The note's path as of this revision; it differs from the current one after a move. */
    val path: String = "",
)

@Serializable
data class HistoryResponse(val entries: List<HistoryEntry> = emptyList())

@Serializable
data class HistoryDiffResponse(val diff: String = "")

@Serializable
data class RestoreNoteRequest(val revision: String, val path: String)

@Serializable
data class OkResponse(val ok: Boolean = false)

/** One note a feed entry touched. [id] and [title] are present when the note still exists. */
@Serializable
data class ActivityChange(
    val action: String = "modified", // added, modified, renamed, deleted
    val path: String = "",
    /** The previous path; renames only. */
    val from: String? = null,
    val id: String? = null,
    val title: String? = null,
)

/** One feed entry: a commit, or a run of commits by one agent in a quiet stretch. */
@Serializable
data class ActivityEntry(
    val author: String = "",
    /** person, agent, or filesystem. */
    val kind: String = "person",
    val from: String = "",
    val to: String = "",
    /** The newest commit of the entry — where a "restore to here" lands. */
    val commit: String = "",
    val commits: Int = 1,
    val changes: List<ActivityChange> = emptyList(),
)

@Serializable
data class ActivityResponse(
    val entries: List<ActivityEntry> = emptyList(),
    @SerialName("next_cursor") val nextCursor: String = "",
    val more: Boolean = false,
    /** Whether this account may restore the space to a feed entry. */
    @SerialName("restore_allowed") val restoreAllowed: Boolean = false,
)

/** One path a point-in-time restore would touch. */
@Serializable
data class PitChange(
    val action: String = "changed", // added, changed, deleted, moved
    val path: String = "",
    /** The path the note holds now; moves only. */
    val from: String? = null,
    val id: String? = null,
    val title: String? = null,
)

/** What restoring to a commit would do, reported before anything moves. */
@Serializable
data class PitPreview(
    val commit: String = "",
    val subject: String = "",
    val author: String = "",
    val date: String = "",
    /** The scope the preview was computed for; "" is the whole tree. */
    val space: String = "",
    val added: Int = 0,
    val changed: Int = 0,
    val deleted: Int = 0,
    val moved: Int = 0,
    val changes: List<PitChange> = emptyList(),
)

@Serializable
data class PitPreviewResponse(val preview: PitPreview? = null)

@Serializable
data class PitRestoreRequest(val commit: String, val space: String)

/** What a restore did: the commit it landed and the tag that holds what stood before. */
@Serializable
data class RestoreSummary(
    val ok: Boolean = false,
    val commit: String = "",
    val tag: String = "",
    val added: Int = 0,
    val changed: Int = 0,
    val deleted: Int = 0,
    val moved: Int = 0,
)

/** One row of the deleted-notes list: a note whose file is gone, with what a restore recovers it from. */
@Serializable
data class DeletedNoteRow(
    val id: String,
    val space: String = "",
    val path: String = "",
    val title: String = "",
    val kind: String = "md",
    val created: String = "",
    @SerialName("deleted_at") val deletedAt: String = "",
    @SerialName("trash_path") val trashPath: String? = null,
    @SerialName("has_file") val hasFile: Boolean = false,
    @SerialName("has_sidecar") val hasSidecar: Boolean = false,
    @SerialName("in_history") val inHistory: Boolean = false,
    val untracked: Boolean = false,
)

@Serializable
data class DeletedNotesResponse(val entries: List<DeletedNoteRow> = emptyList())

/** What bringing one deleted note back did. [from] says where the content came from: the trash, or the history. */
@Serializable
data class DeletedRestoreResult(
    val ok: Boolean = false,
    val path: String = "",
    val conflict: Boolean = false,
    val note: Note? = null,
    /** True when the scan has not picked the note up yet; there is nothing to open. */
    val deferred: Boolean = false,
    val from: String = "",
)

/** The trash listing's answer: GET /api/trash. The rows are the same
 * deleted-note shape the deleted-notes list reads; the trash's own
 * fields (has_file, has_sidecar, untracked) ride on it, and
 * in_history stays false — the trash speaks for its own window. */
@Serializable
data class TrashResponse(val entries: List<DeletedNoteRow> = emptyList())

/** What a restore from the trash did: where the note landed, whether
 * something new had taken its path, and the re-indexed note when the
 * scan has already picked it up. */
@Serializable
data class TrashRestoreResult(
    val ok: Boolean = false,
    val path: String = "",
    val conflict: Boolean = false,
    val note: Note? = null,
    /** True when the scan has not picked the note up yet; there is nothing to open. */
    val deferred: Boolean = false,
)

/** What emptying the trash did: how many entries were destroyed for good. */
@Serializable
data class TrashEmptyResult(val ok: Boolean = false, val destroyed: Int = 0)

/** Makes a folder: POST /api/dirs. The path names a folder inside a space. */
@Serializable
data class DirCreateRequest(val path: String)

@Serializable
data class DirCreateResponse(val path: String = "")

/** Moves or renames a folder: POST /api/dirs/move — every note under
 * it moves with it, its inbound links rewritten. */
@Serializable
data class DirMoveRequest(val path: String, val to: String)

@Serializable
data class DirMoveResponse(
    val path: String = "",
    val moved: Int = 0,
    val total: Int = 0,
    val rewritten: Int = 0,
    val broken: Int = 0,
)

/** Deleting a folder trashes its notes: DELETE /api/dirs?path=. */
@Serializable
data class DirDeleteResponse(
    val ok: Boolean = false,
    val deleted: Int = 0,
    val removed: Boolean = false,
)

/** A note's soft delete: the file moves under the trash for thirty days. */
@Serializable
data class NoteDeleteResponse(
    val ok: Boolean = false,
    @SerialName("trash_path") val trashPath: String = "",
)

/** What moving a note did on the server: its row at the new path and
 * the links the move rewrote. */
@Serializable
data class MoveNoteResult(
    val note: Note? = null,
    val rewritten: Int = 0,
    val broken: Int = 0,
)

/** One row of the conflicts list: a copy, and the note it belongs to while that survives. */
@Serializable
data class ConflictEntry(
    val note: NoteMeta,
    /** Omitted when the copy's original is gone; it is a plain note now. */
    val of: NoteMeta? = null,
)

@Serializable
data class ConflictsResponse(val conflicts: List<ConflictEntry> = emptyList())

@Serializable
data class NoteConflictsResponse(val conflicts: List<NoteMeta> = emptyList())

/** One side of a conflict diff: the survivor is mine, the copy is theirs. */
@Serializable
data class ConflictSide(
    val id: String = "",
    val path: String = "",
    val title: String = "",
)

@Serializable
data class ConflictDiffResponse(
    val diff: String = "",
    val mine: ConflictSide = ConflictSide(),
    val theirs: ConflictSide = ConflictSide(),
)

/** The three ways out of a conflict: keep this note, keep the copy, keep both. */
enum class ConflictAction(val value: String) {
    Mine("mine"),
    Theirs("theirs"),
    Both("both"),
}

@Serializable
data class ConflictResolveRequest(val action: String)

/** What a resolution did; [path] is the copy's renamed path, keep-both only. */
@Serializable
data class ConflictResolveResponse(
    val ok: Boolean = false,
    val action: String = "",
    val path: String? = null,
)
