package io.github.acip.minimedia.ui

import android.app.Application
import android.view.KeyEvent as NativeKeyEvent
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import io.github.acip.minimedia.data.AppGraph
import io.github.acip.minimedia.data.Discovery
import io.github.acip.minimedia.data.DiscoveryReply
import io.github.acip.minimedia.data.Session
import io.github.acip.minimedia.graph
import kotlinx.coroutines.launch

sealed interface ConnectState {
    data class Working(val message: String) : ConnectState
    data class Choose(val servers: List<DiscoveryReply>) : ConnectState
    data class Manual(val error: String?, val prefill: String) : ConnectState
}

class ConnectViewModel(app: Application) : AndroidViewModel(app) {
    private val graph = app.graph
    var state by mutableStateOf<ConnectState>(ConnectState.Working("Looking for your MiniMedia server…"))
        private set
    var connected by mutableStateOf(false)
        private set
    private var started = false

    fun start(forget: Boolean) {
        if (started) return
        started = true
        viewModelScope.launch { run(forget) }
    }

    fun rediscover() = viewModelScope.launch { run(forget = false, skipSaved = true) }

    private suspend fun run(forget: Boolean, skipSaved: Boolean = false) {
        if (forget) graph.forgetServer()
        val saved = graph.savedServer()
        if (saved != null && !skipSaved) {
            state = ConnectState.Working("Connecting to ${saved.name.ifEmpty { "your server" }}…")
            val s = tryConnect(saved.url)
            if (s != null && (saved.serverId.isEmpty() || s.info.serverId == saved.serverId)) return done(s)
        }
        state = ConnectState.Working("Looking for MiniMedia servers on your network…")
        val replies = Discovery.discover(getApplication(), graph.json)
        // The remembered server, maybe at a new address (PLAN.md 8.2).
        saved?.let { sv -> replies.firstOrNull { it.serverId == sv.serverId } }?.let { r ->
            AppGraph.normalizeUrl(r.url)?.let { url -> tryConnect(url)?.let { return done(it) } }
        }
        val prefill = saved?.url?.removePrefix("http://")?.trimEnd('/') ?: ""
        when (replies.size) {
            0 -> state = ConnectState.Manual(null, prefill)
            1 -> {
                val url = AppGraph.normalizeUrl(replies[0].url)
                val s = url?.let { tryConnect(it) }
                if (s != null) done(s) else state = ConnectState.Manual("Found ${replies[0].name} but could not connect to ${replies[0].url}", prefill)
            }
            else -> state = ConnectState.Choose(replies)
        }
    }

    fun choose(r: DiscoveryReply) = viewModelScope.launch {
        state = ConnectState.Working("Connecting to ${r.name}…")
        val s = AppGraph.normalizeUrl(r.url)?.let { tryConnect(it) }
        if (s != null) done(s) else state = ConnectState.Manual("Could not connect to ${r.url}", r.url.removePrefix("http://"))
    }

    fun manual(input: String) = viewModelScope.launch {
        val url = AppGraph.normalizeUrl(input)
        if (url == null) {
            state = ConnectState.Manual("Enter an address like 192.168.1.20:8096", input)
            return@launch
        }
        state = ConnectState.Working("Connecting to $input…")
        val s = tryConnect(url)
        if (s != null) done(s) else state = ConnectState.Manual("No MiniMedia server answered at $input", input)
    }

    private suspend fun tryConnect(url: String): Session? = runCatching { graph.connect(url) }.getOrNull()

    private suspend fun done(s: Session) {
        graph.saveServer(s)
        connected = true
    }
}

@Composable
fun ConnectScreen(forget: Boolean, onConnected: () -> Unit, vm: ConnectViewModel = viewModel()) {
    LaunchedEffect(Unit) { vm.start(forget) }
    LaunchedEffect(vm.connected) { if (vm.connected) onConnected() }

    Box(Modifier.fillMaxSize().padding(64.dp), contentAlignment = Alignment.Center) {
        when (val s = vm.state) {
            is ConnectState.Working -> CenteredMessage(s.message)
            is ConnectState.Choose -> ChooseServer(s.servers, vm::choose, vm::rediscover)
            is ConnectState.Manual -> ManualEntry(s, vm::manual, vm::rediscover)
        }
    }
}

@Composable
private fun ChooseServer(servers: List<DiscoveryReply>, onChoose: (DiscoveryReply) -> Unit, onRediscover: () -> Unit) {
    val first = remember { FocusRequester() }
    Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(16.dp)) {
        Text("Choose a server", style = MaterialTheme.typography.headlineMedium)
        servers.forEachIndexed { i, r ->
            ActionButton(
                "${r.name}  ·  ${r.url.removePrefix("http://")}",
                onClick = { onChoose(r) },
                modifier = if (i == 0) Modifier.focusRequester(first) else Modifier,
            )
        }
        ActionButton("Search again", onClick = onRediscover)
    }
    LaunchedEffect(Unit) { runCatching { first.requestFocus() } }
}

/** Address entry with an on-screen keypad (digits, '.', ':'), easy with a remote. */
@Composable
private fun ManualEntry(state: ConnectState.Manual, onConnect: (String) -> Unit, onRediscover: () -> Unit) {
    var text by remember(state) { mutableStateOf(state.prefill) }
    val first = remember { FocusRequester() }
    val keys = listOf("1", "2", "3", "4", "5", "6", "7", "8", "9", ".", "0", ":")

    Row(
        horizontalArrangement = Arrangement.spacedBy(48.dp),
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier.onPreviewKeyEvent { e ->
            // Hardware/IME characters (e.g. `adb shell input text`) also type.
            if (e.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
            val native = e.nativeKeyEvent
            val c = native.unicodeChar.toChar()
            when {
                c.isDigit() || c == '.' || c == ':' -> { text += c; true }
                native.keyCode == NativeKeyEvent.KEYCODE_DEL -> { text = text.dropLast(1); true }
                native.keyCode == NativeKeyEvent.KEYCODE_ENTER && text.isNotEmpty() -> { onConnect(text); true }
                else -> false
            }
        },
    ) {
        Column(Modifier.width(420.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            Text("Connect to your MiniMedia server", style = MaterialTheme.typography.headlineSmall)
            Text(
                state.error ?: "No server was found automatically. Enter the address of the PC running MiniMedia.",
                style = MaterialTheme.typography.bodyMedium,
                color = if (state.error != null) Color(0xFFFF8A80) else Muted,
            )
            Box(
                Modifier.width(420.dp).height(52.dp).border(2.dp, Color(0x66FFFFFF), RoundedCornerShape(8.dp))
                    .background(Color(0x22FFFFFF), RoundedCornerShape(8.dp)).padding(horizontal = 16.dp),
                contentAlignment = Alignment.CenterStart,
            ) {
                Text(text.ifEmpty { "192.168.1.20:8096" }, style = MaterialTheme.typography.titleLarge,
                    fontFamily = FontFamily.Monospace, color = if (text.isEmpty()) Color(0x66FFFFFF) else Color.White)
            }
            Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                ActionButton("Connect", onClick = { onConnect(text) }, enabled = text.isNotEmpty())
                ActionButton("Search again", onClick = onRediscover)
            }
        }
        Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
            (keys + "⌫").plus(listOf()).chunked(3).let { rows ->
                rows.forEachIndexed { row, chunk ->
                    Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                        chunk.forEachIndexed { col, k ->
                            val mod = if (row == 0 && col == 0) Modifier.focusRequester(first) else Modifier
                            KeyButton(k, mod) { text = if (k == "⌫") text.dropLast(1) else text + k }
                        }
                    }
                }
            }
        }
    }
    LaunchedEffect(Unit) { runCatching { first.requestFocus() } }
}

@Composable
private fun KeyButton(label: String, modifier: Modifier, onClick: () -> Unit) {
    androidx.tv.material3.Button(
        onClick = onClick,
        modifier = modifier.size(width = 72.dp, height = 48.dp),
        contentPadding = androidx.compose.foundation.layout.PaddingValues(0.dp),
        colors = androidx.tv.material3.ButtonDefaults.colors(
            containerColor = Color(0x33FFFFFF), contentColor = Color.White,
            focusedContainerColor = Color.White, focusedContentColor = Color.Black,
        ),
    ) {
        Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
            Text(label, style = MaterialTheme.typography.titleLarge)
        }
    }
}
