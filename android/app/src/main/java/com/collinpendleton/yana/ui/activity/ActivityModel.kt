package com.collinpendleton.yana.ui.activity

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.collinpendleton.yana.Prefs
import com.collinpendleton.yana.data.NoteRepository
import com.collinpendleton.yana.data.Space
import com.collinpendleton.yana.data.userMessage
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/** What the feed shows: the merged page, or why there is none. */
data class ActivityState(
    val rows: List<ActivityRow>? = null,
    val loading: Boolean = true,
    val refreshing: Boolean = false,
    val error: String? = null,
    /** Whether older history remains under the page shown. */
    val more: Boolean = false,
    /** Whether the shown space's feed allows restoring to an entry (the tree scope is owner-only, decided at the screen). */
    val restoreAllowed: Boolean = false,
)

/**
 * The feed's half: one merged page per space and window, paged by each
 * space's own cursor, with the "since I last looked" marker kept per
 * device — captured when the screen opens, advanced only by a page
 * that loaded.
 */
class ActivityModel : ViewModel() {
    /** "" merges every space the account belongs to. */
    val scope = MutableStateFlow("")
    val window = MutableStateFlow(FeedWindow.Seen)
    val spaces = MutableStateFlow<List<Space>>(emptyList())

    private val _state = MutableStateFlow(ActivityState())
    val state: StateFlow<ActivityState> = _state.asStateFlow()

    /** The marker as it was when the screen opened; the divider sits under everything newer than it. */
    var seenAt: Long? = null
        private set

    private var marked = false
    private var started = false
    private var prefs: Prefs? = null
    private var cursors: Map<String, String> = emptyMap()
    private var load: Job? = null

    fun start(repo: NoteRepository, prefs: Prefs, initialSpace: String) {
        if (started) return
        started = true
        this.prefs = prefs
        seenAt = prefs.activitySeen()
        window.value = if (seenAt != null) FeedWindow.Seen else FeedWindow.Week
        scope.value = initialSpace
        viewModelScope.launch {
            runCatching { repo.spaces() }.onSuccess { spaces.value = it }
            load(repo)
        }
    }

    /** The spaces the page fetches: one, or every named space the account has (the root has no feed of its own). */
    private fun targets(): List<String> =
        (if (scope.value.isNotEmpty()) listOf(scope.value) else spaces.value.map { it.name })
            .filter { it.isNotEmpty() }
            .distinct()

    /** Fetches the first page of the current scope and window. */
    fun load(repo: NoteRepository, pull: Boolean = false) {
        load?.cancel()
        cursors = emptyMap()
        _state.value = _state.value.copy(loading = _state.value.rows == null, refreshing = pull, error = null)
        fetch(repo, pull = pull)
    }

    /** Fetches the next page under the cursors the last one left. */
    fun older(repo: NoteRepository) {
        if (load?.isActive == true) return
        fetch(repo, append = true)
    }

    private fun fetch(repo: NoteRepository, pull: Boolean = false, append: Boolean = false) {
        val targets = targets()
        load = viewModelScope.launch {
            _state.value = _state.value.copy(
                loading = !append && _state.value.rows == null,
                refreshing = pull || append,
                error = null,
            )
            try {
                val since = sinceIso(window.value, seenAt)
                val pages = targets.map { space ->
                    async {
                        space to repo.activity(
                            space = space,
                            since = since,
                            cursor = if (append) cursors[space]?.ifEmpty { null } else null,
                            limit = 50,
                        )
                    }
                }.awaitAll()
                val merged = mergePages(pages)
                cursors = merged.cursors
                // The visit itself is the marker: next time, everything
                // here is old news. Only a page that loaded counts.
                if (!marked) {
                    marked = true
                    this@ActivityModel.prefs?.touchActivitySeen()
                }
                _state.value = ActivityState(
                    rows = if (append) (_state.value.rows ?: emptyList()) + merged.rows else merged.rows,
                    loading = false,
                    more = merged.more,
                    restoreAllowed = pages.firstOrNull { it.first == scope.value }?.second?.restoreAllowed ?: false,
                )
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                _state.value = _state.value.copy(loading = false, refreshing = false, error = e.userMessage())
            }
        }
    }

    fun filter(repo: NoteRepository, space: String) {
        if (scope.value == space) return
        scope.value = space
        _state.value = ActivityState()
        load(repo)
    }

    fun setWindow(repo: NoteRepository, w: FeedWindow) {
        if (window.value == w) return
        window.value = w
        _state.value = ActivityState()
        load(repo)
    }

    /** After a restore changed the tree: reload the page and the replica behind the tree screens. */
    fun restored(repo: NoteRepository) {
        load(repo, pull = true)
        viewModelScope.launch { runCatching { repo.sync() } }
    }
}
