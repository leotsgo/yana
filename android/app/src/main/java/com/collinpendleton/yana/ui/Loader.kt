package com.collinpendleton.yana.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import com.collinpendleton.yana.data.userMessage

/** What a screen that reads one thing from the server shows. */
data class Loaded<T>(
    val data: T? = null,
    val loading: Boolean = true,
    val refreshing: Boolean = false,
    val error: String? = null,
)

/**
 * Loads once when created and again on pull-to-refresh, keeping the last
 * good data on screen while a refresh runs or fails. The first load and
 * a refresh can read differently — the replica answers the first (so the
 * screen works offline) while a refresh goes to the server.
 */
class Loader<T>(
    private val fetch: suspend () -> T,
    private val refetch: (suspend () -> T)? = null,
    /** A local answer shown before the first fetch returns, when there is one. */
    private val cached: (suspend () -> T?)? = null,
) : ViewModel() {
    private val state = MutableStateFlow(Loaded<T>())
    val loaded: StateFlow<Loaded<T>> = state.asStateFlow()
    private var job: Job? = null

    init {
        reload()
    }

    fun reload(pull: Boolean = false) {
        job?.cancel()
        state.value = state.value.copy(loading = state.value.data == null, refreshing = pull, error = null)
        job = viewModelScope.launch {
            if (!pull && state.value.data == null) {
                val local = runCatching { cached?.invoke() }.getOrNull()
                if (local != null) state.value = Loaded(data = local, loading = false)
            }
            state.value = try {
                val data = if (pull) (refetch?.invoke() ?: fetch()) else fetch()
                Loaded(data = data, loading = false)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                state.value.copy(loading = false, refreshing = false, error = e.userMessage())
            }
        }
    }
}
