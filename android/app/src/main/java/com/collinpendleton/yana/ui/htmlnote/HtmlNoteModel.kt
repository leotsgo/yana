package com.collinpendleton.yana.ui.htmlnote

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.collinpendleton.yana.data.NoteRepository
import com.collinpendleton.yana.data.userMessage
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import retrofit2.HttpException

/** The rendered-view half of an HTML note: the minted URL, or why there is none. */
data class HtmlViewState(
    val url: String? = null,
    val loading: Boolean = true,
    val message: String? = null,
)

/**
 * Mints the content-origin view URL for a note and saves its source.
 * Tokens live five minutes, so every open and every save mints a fresh
 * one; a mint that fails keeps the last good URL on screen with a
 * message beside it.
 */
class HtmlNoteModel : ViewModel() {
    private val _view = MutableStateFlow(HtmlViewState())
    val view: StateFlow<HtmlViewState> = _view.asStateFlow()

    val saving = MutableStateFlow(false)
    val message = MutableStateFlow<String?>(null)

    private var mint: Job? = null

    fun load(repo: NoteRepository, id: String) {
        mint?.cancel()
        _view.value = _view.value.copy(loading = _view.value.url == null)
        mint = viewModelScope.launch {
            try {
                _view.value = HtmlViewState(url = repo.noteView(id).url)
            } catch (e: CancellationException) {
                throw e
            } catch (e: HttpException) {
                _view.value = if (e.code() == 501) {
                    HtmlViewState(loading = false, message = "This server runs without the content origin; the source is shown as text.")
                } else {
                    _view.value.copy(loading = false, message = e.userMessage())
                }
            } catch (e: Exception) {
                _view.value = if (_view.value.url == null) {
                    HtmlViewState(loading = false, message = "The rendered view needs the server; the source is shown as text.")
                } else {
                    _view.value.copy(loading = false, message = "The view could not be refreshed.")
                }
            }
        }
    }

    /**
     * Saves the source, last-write-wins; [onSaved] gets the new base
     * hash and any conflict copy the server parked. The pane makes the
     * copy its own notice with a way in to resolve it, so the model's
     * message stays for errors.
     */
    fun save(repo: NoteRepository, id: String, source: String, baseHash: String, onSaved: (hash: String?, conflictCopy: String?) -> Unit) {
        if (saving.value) return
        saving.value = true
        viewModelScope.launch {
            try {
                val res = repo.saveSource(id, source, baseHash)
                saving.value = false
                message.value = null
                onSaved(res.hash, res.conflictCopy)
                load(repo, id)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                saving.value = false
                message.value = e.userMessage()
            }
        }
    }
}
