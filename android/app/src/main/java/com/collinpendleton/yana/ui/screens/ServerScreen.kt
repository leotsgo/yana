package com.collinpendleton.yana.ui.screens

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.collinpendleton.yana.data.YanaClient
import com.collinpendleton.yana.data.normalizeServerUrl
import com.collinpendleton.yana.data.userMessage
import com.collinpendleton.yana.ui.BrandMark
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

/** First run: which server this phone talks to. */
@Composable
fun ServerScreen(client: YanaClient, onReady: (server: String, setup: Boolean) -> Unit) {
    var address by rememberSaveable { mutableStateOf(client.lastServer ?: "") }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()
    val parsed = normalizeServerUrl(address)

    fun connect() {
        val url = normalizeServerUrl(address)
        if (url == null) {
            error = "Enter an address like notes.example.com or http://192.168.1.20:8080."
            return
        }
        busy = true
        error = null
        scope.launch {
            try {
                val state = client.state(url)
                onReady(url.toString(), state.setupRequired)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                error = e.userMessage()
            } finally {
                busy = false
            }
        }
    }

    Surface(Modifier.fillMaxSize()) {
        Column(
            Modifier
                .safeDrawingPadding()
                .imePadding()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 24.dp, vertical = 48.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Column(Modifier.widthIn(max = 420.dp).fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                BrandMark()
                Spacer(Modifier.height(20.dp))
                Text("Connect to your server", style = MaterialTheme.typography.headlineSmall)
                Text(
                    "Enter the address you open YANA/ at in a browser.",
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                OutlinedTextField(
                    value = address,
                    onValueChange = { address = it; error = null },
                    label = { Text("Server address") },
                    placeholder = { Text("notes.example.com") },
                    singleLine = true,
                    enabled = !busy,
                    isError = error != null,
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Go, autoCorrectEnabled = false),
                    keyboardActions = KeyboardActions(onGo = { connect() }),
                    modifier = Modifier.fillMaxWidth().testTag("server-address"),
                )
                if (parsed?.isHttps == false) {
                    Text(
                        "This address uses plain HTTP: your password and notes travel unencrypted. Use it on a network you trust.",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                error?.let { Text(it, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.error) }
                Button(onClick = ::connect, enabled = !busy && address.isNotBlank(), modifier = Modifier.fillMaxWidth().height(48.dp)) {
                    if (busy) CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp, color = MaterialTheme.colorScheme.onPrimary)
                    else Text("Continue")
                }
            }
        }
    }
}
