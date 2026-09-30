package com.collinpendleton.yana.ui.tasks

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.collinpendleton.yana.data.NoteRepository
import com.collinpendleton.yana.data.Space
import com.collinpendleton.yana.data.TagCount
import com.collinpendleton.yana.data.TaskRow
import com.collinpendleton.yana.data.TaskScope
import com.collinpendleton.yana.data.TickOutcome
import com.collinpendleton.yana.data.TasksResult
import com.collinpendleton.yana.data.userMessage
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/** What the tasks screen shows: the listing for its filters, or why there is none. */
data class TasksState(
    val rows: List<TaskRow>? = null,
    val loading: Boolean = true,
    val refreshing: Boolean = false,
    val error: String? = null,
    /** False when the rows came from the cache with the network gone. */
    val fromServer: Boolean = true,
    /** When the shown rows were fetched, epoch ms; the cache's age offline. */
    val fetchedAt: Long? = null,
)

/**
 * The tasks screen's half: one listing per filter scope, ticked in place
 * optimistically the way the web's page ticks, the filters' choices
 * (spaces, tags, folders), and the reconnect pass that replays a queued
 * tick before refetching.
 */
class TasksModel : ViewModel() {
    val scope = MutableStateFlow(TaskScope())
    val spaces = MutableStateFlow<List<Space>>(emptyList())
    val tags = MutableStateFlow<List<TagCount>>(emptyList())
    val folders = MutableStateFlow<List<String>>(emptyList())

    private val _state = MutableStateFlow(TasksState())
    val state: StateFlow<TasksState> = _state.asStateFlow()

    /** One tick's outcome, for the snackbar: the message, and Undo for a taken one. */
    data class TickFeedback(val task: TaskRow, val to: Boolean, val outcome: TickOutcome)

    val feedback = MutableSharedFlow<TickFeedback>(extraBufferCapacity = 16)

    private var load: Job? = null

    /** The spaces the live watch covers: one, or every space the account has. */
    val watchTargets: List<String>
        get() = (if (scope.value.space.isNotEmpty()) listOf(scope.value.space) else spaces.value.map { it.name })
            .filter { it.isNotEmpty() }

    fun start(repo: NoteRepository, space: String) {
        if (scope.value != TaskScope()) return // already running (a config change)
        scope.value = TaskScope(space = space)
        viewModelScope.launch {
            runCatching { repo.spaces() }.onSuccess { spaces.value = it }
            runCatching { repo.tags() }.onSuccess { tags.value = it }
        }
        load(repo)
    }

    /** Fetches the scope's listing, keeping the last good rows on a failure. */
    fun load(repo: NoteRepository, pull: Boolean = false) {
        load?.cancel()
        _state.value = _state.value.copy(loading = _state.value.rows == null, refreshing = pull, error = null)
        load = viewModelScope.launch {
            _state.value = try {
                val res: TasksResult = repo.tasks(scope.value)
                if (scope.value.path.isEmpty()) refreshFolders(res.rows)
                TasksState(rows = res.rows, loading = false, fromServer = res.fromServer, fetchedAt = res.fetchedAt)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                _state.value.copy(loading = false, refreshing = false, error = e.userMessage())
            }
        }
    }

    /**
     * The reconnect pass: replay whatever the queue holds (a tick from
     * airplane mode; the server's PATCH is idempotent, so the replay of
     * an applied tick writes nothing), then refetch.
     */
    fun reconnected(repo: NoteRepository) {
        viewModelScope.launch {
            runCatching { repo.sync() }
            load(repo)
        }
    }

    fun filter(repo: NoteRepository, space: String = scope.value.space, tag: String = scope.value.tag, path: String = scope.value.path, done: Boolean = scope.value.done) {
        val next = TaskScope(space = space, tag = tag, path = path, done = done)
        if (next == scope.value) return
        scope.value = next
        if (path.isEmpty()) folders.value = emptyList() // the folder list follows the next listing
        load(repo)
    }

    /**
     * Ticks one box in place, optimistically: the row moves now, the
     * write lands or the row reverts with its reason said. A 409 means
     * the line moved under the list; the listing refetches, the way the
     * server's message says it catches up on its own.
     */
    fun tick(repo: NoteRepository, task: TaskRow, to: Boolean) {
        val rows = _state.value.rows ?: return
        if (task.done == to) return
        val row: (TaskRow) -> Boolean = { it.note.id == task.note.id && it.line == task.line }
        _state.value = _state.value.copy(rows = rows.map { if (row(it)) it.copy(done = to) else it })
        viewModelScope.launch {
            val outcome = repo.tickTask(task.note.id, task.line, to)
            if (outcome is TickOutcome.Refused) {
                _state.value = _state.value.copy(rows = _state.value.rows?.map { if (row(it)) it.copy(done = !to) else it })
            }
            feedback.tryEmit(TickFeedback(task, to, outcome))
            if (outcome is TickOutcome.Refused && outcome.status == 409) load(repo)
        }
    }

    /** The folders a filter can pick, from the listing's own paths — stable once a folder is chosen. */
    private fun refreshFolders(rows: List<TaskRow>) {
        val dirs = rows
            .map { it.note.path.substringBeforeLast('/', "") }
            .filter { it.contains('/') }
            .toSortedSet()
        folders.value = dirs.toList()
    }
}
