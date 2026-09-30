package com.collinpendleton.yana.data

import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.PATCH
import retrofit2.http.POST
import retrofit2.http.PUT
import retrofit2.http.Path
import retrofit2.http.Query

/** The sign-in endpoints: no bearer token, never retried on a 401. */
interface AuthApi {
    @GET("api/auth/state")
    suspend fun state(): AuthState

    @POST("api/auth/setup")
    suspend fun setup(@Body body: Credentials): SignInResponse

    @POST("api/auth/login")
    suspend fun login(@Body body: Credentials): SignInResponse

    @POST("api/auth/logout")
    suspend fun logout(@Body body: RefreshRequest)
}

/** Everything behind the bearer token that the navigation shell reads. */
interface YanaApi {
    @GET("api/spaces")
    suspend fun spaces(): SpacesResponse

    /** One space's tree, or every visible space when [space] is null. */
    @GET("api/tree")
    suspend fun tree(@Query("space") space: String? = null): TreeResponse

    /**
     * The flat note list the offline replica syncs from: every visible
     * note with its metadata and tags, or just one space's when given.
     */
    @GET("api/notes")
    suspend fun notes(@Query("space") space: String? = null): NotesResponse

    @GET("api/notes/{id}")
    suspend fun note(@Path("id") id: String): Note

    /** The signed content-origin URL an HTML note renders in. */
    @GET("api/notes/{id}/view")
    suspend fun noteView(@Path("id") id: String): NoteView

    /** The signed content-origin URL one attachment opens in (PDFs render there). */
    @GET("api/notes/{id}/asset-view")
    suspend fun assetView(
        @Path("id") id: String,
        @Query("asset") asset: String,
    ): NoteView

    /** The note's live public link, or null when there is none. */
    @GET("api/notes/{id}/public-link")
    suspend fun publicLink(@Path("id") id: String): PublicLinkResponse

    /** Makes (or returns) the note's public link; `expires` sets a new link's life. */
    @POST("api/notes/{id}/public-link")
    suspend fun createPublicLink(
        @Path("id") id: String,
        @Body body: PublicLinkRequest,
    ): PublicLinkCreateResponse

    /** Retires the note's public link; revoking a note with none succeeds. */
    @DELETE("api/notes/{id}/public-link")
    suspend fun revokePublicLink(@Path("id") id: String): OkResponse

    /** Full-text search with the operator grammar, the server's half. */
    @GET("api/search")
    suspend fun search(
        @Query("q") query: String,
        @Query("space") space: String? = null,
        @Query("limit") limit: Int = 50,
    ): SearchResponse

    @POST("api/notes")
    suspend fun createNote(@Body body: CreateNoteRequest): CreateNoteResponse

    /** Opens today's daily note, making it from the template when missing. */
    @POST("api/notes/daily")
    suspend fun daily(@Body body: DailyNoteRequest): DailyNoteResponse

    /** The server's status; capture reads the daily-note pattern. */
    @GET("api/status")
    suspend fun status(): ServerStatus

    /** Ticks one task box through the server's CRDT write. */
    @PATCH("api/tasks")
    suspend fun tickTask(@Body body: TaskTickRequest)

    /**
     * Every task the filters name: open boxes by default, completed
     * ones for the last 30 days with done=true.
     */
    @GET("api/tasks")
    suspend fun tasks(
        @Query("space") space: String? = null,
        @Query("done") done: Boolean? = null,
        @Query("tag") tag: String? = null,
        @Query("path") path: String? = null,
    ): TasksResponse

    /** The open count across every space the account belongs to. */
    @GET("api/tasks")
    suspend fun taskCount(@Query("count") count: Int = 1): TaskCountResponse

    /** The account's tags with their note counts. */
    @GET("api/tags")
    suspend fun tags(): TagsResponse

    /** The notes carrying one tag, in path order. */
    @GET("api/tags/{tag}")
    suspend fun tagNotes(@Path("tag") tag: String): TagNotesResponse

    /** The notes linking to one, with the line each link sits on. */
    @GET("api/notes/{id}/backlinks")
    suspend fun backlinks(@Path("id") id: String): BacklinksResponse

    @POST("api/notes/{id}/move")
    suspend fun moveNote(@Path("id") id: String, @Body body: MoveRequest): MoveNoteResult

    /** Deletes a note: the file moves under the trash for thirty days. */
    @DELETE("api/notes/{id}")
    suspend fun deleteNote(@Path("id") id: String): NoteDeleteResponse

    /** Makes an empty folder; it shows in the tree at once. */
    @POST("api/dirs")
    suspend fun createDir(@Body body: DirCreateRequest): DirCreateResponse

    /** Moves or renames a folder: every note under it moves, links rewritten. */
    @POST("api/dirs/move")
    suspend fun moveDir(@Body body: DirMoveRequest): DirMoveResponse

    /** Deletes a folder: its notes go to the trash, the empty shell goes. */
    @DELETE("api/dirs")
    suspend fun deleteDir(@Query("path") path: String): DirDeleteResponse

    /** Every deleted note with something to bring it back, and its actions. */
    @GET("api/trash")
    suspend fun trash(): TrashResponse

    /** Returns one deleted note to its original path, or beside its new occupant. */
    @POST("api/trash/{id}/restore")
    suspend fun restoreTrash(@Path("id") id: String): TrashRestoreResult

    /** Destroys one trash entry for good; this cannot be undone. */
    @DELETE("api/trash/{id}")
    suspend fun destroyTrash(@Path("id") id: String): OkResponse

    /** Destroys every trash entry the caller may write. */
    @POST("api/trash/empty")
    suspend fun emptyTrash(): TrashEmptyResult

    /**
     * PUT one file under an `_assets` directory; the path is
     * percent-encoded per segment and the server picks a free name.
     */
    @PUT("api/files/{path}")
    suspend fun uploadFile(
        @Path(value = "path", encoded = true) path: String,
        @Body body: okhttp3.RequestBody,
    ): UploadResponse

    /** Saves an HTML note's source, whole-file and last-write-wins. */
    @PUT("api/notes/{id}/source")
    suspend fun saveSource(@Path("id") id: String, @Body body: SaveSourceRequest): SaveSourceResponse

    @GET("api/auth/sessions")
    suspend fun sessions(): SessionsResponse

    /** Ends one session; the device it belongs to signs out on its next request. */
    @DELETE("api/auth/sessions/{id}")
    suspend fun revokeSession(@Path("id") id: String): OkResponse

    /** The accounts on this server; the owner only. */
    @GET("api/users")
    suspend fun users(): UsersResponse

    /** Adds an account with a starting password; the owner only. */
    @POST("api/users")
    suspend fun createUser(@Body body: CreateUserRequest): UserInfo

    /** Removes an account; the owner only, never the owner's own. */
    @DELETE("api/users/{id}")
    suspend fun deleteUser(@Path("id") id: String): OkResponse

    /** Sets a password: your own, or another person's as the owner. */
    @POST("api/users/{id}/password")
    suspend fun setPassword(@Path("id") id: String, @Body body: PasswordRequest): OkResponse

    /** One space as the caller sees it: the role, and the members for a space owner. */
    @GET("api/spaces/{space}")
    suspend fun spaceDetail(@Path("space") space: String): SpaceDetail

    /** Replaces a space's label and member list; a space owner's write. */
    @PATCH("api/spaces/{space}")
    suspend fun updateSpace(@Path("space") space: String, @Body body: UpdateSpaceRequest): OkResponse

    /** Makes a space; any signed-in account may, and becomes its owner. */
    @POST("api/spaces")
    suspend fun createSpace(@Body body: CreateSpaceRequest): CreateSpaceResponse

    /** Makes sure the Start here note exists and answers with it. */
    @POST("api/guide")
    suspend fun guide(@Body body: GuideRequest): GuideResponse

    /** The note's revisions from the server's git history, renames followed. */
    @GET("api/notes/{id}/history")
    suspend fun noteHistory(@Path("id") id: String): HistoryResponse

    /** The unified diff of the note's path between two of its revisions. */
    @GET("api/notes/{id}/history/diff")
    suspend fun noteHistoryDiff(
        @Path("id") id: String,
        @Query("from") from: String,
        @Query("to") to: String,
    ): HistoryDiffResponse

    /** Writes a revision's old text back into the note as a live edit. */
    @POST("api/notes/{id}/history/restore")
    suspend fun restoreNote(@Path("id") id: String, @Body body: RestoreNoteRequest): OkResponse

    /** One space's history folded into feed entries. */
    @GET("api/spaces/{space}/activity")
    suspend fun activity(
        @Path("space") space: String,
        @Query("path") path: String? = null,
        @Query("since") since: String? = null,
        @Query("author") author: String? = null,
        @Query("limit") limit: Int = 50,
        @Query("cursor") cursor: String? = null,
    ): ActivityResponse

    /** What restoring to a commit would do, exactly, before anything moves. */
    @POST("api/git/restore/preview")
    suspend fun pitPreview(@Body body: PitRestoreRequest): PitPreviewResponse

    /** Moves the tree, or one space, back to a commit. */
    @POST("api/git/restore")
    suspend fun pitRestore(@Body body: PitRestoreRequest): RestoreSummary

    /** Every deleted note with something to bring it back. */
    @GET("api/deleted-notes")
    suspend fun deletedNotes(): DeletedNotesResponse

    /** Brings one deleted note back. */
    @POST("api/deleted-notes/{id}/restore")
    suspend fun restoreDeleted(@Path("id") id: String): DeletedRestoreResult

    /** Every conflict copy in the caller's spaces, with its survivor while that lives. */
    @GET("api/conflicts")
    suspend fun conflicts(): ConflictsResponse

    /** The conflict copies parked beside one note, for the banner on it. */
    @GET("api/notes/{id}/conflicts")
    suspend fun noteConflicts(@Path("id") id: String): NoteConflictsResponse

    /** The diff between one conflict copy and the note it belongs to. */
    @GET("api/conflicts/{id}/diff")
    suspend fun conflictDiff(@Path("id") id: String): ConflictDiffResponse

    /** Settles one conflict copy: keep mine, keep theirs, or keep both. */
    @POST("api/conflicts/{id}/resolve")
    suspend fun resolveConflict(
        @Path("id") id: String,
        @Body body: ConflictResolveRequest,
    ): ConflictResolveResponse
}
