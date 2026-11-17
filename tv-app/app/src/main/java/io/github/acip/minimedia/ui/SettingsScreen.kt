package io.github.acip.minimedia.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import io.github.acip.minimedia.BuildConfig
import io.github.acip.minimedia.data.PlayerSettings
import io.github.acip.minimedia.data.Session
import io.github.acip.minimedia.graph
import kotlinx.coroutines.launch

/** Settings (PLAN.md 8.3): server, subtitle appearance, playback, about. */
@Composable
fun SettingsScreen(session: Session, onChangeServer: () -> Unit, entry: FocusRequester = remember { FocusRequester() }) {
    val graph = LocalContext.current.graph
    val s by graph.playerSettings.settings.collectAsState()
    val scope = rememberCoroutineScope()
    fun update(t: (PlayerSettings) -> PlayerSettings) = scope.launch { graph.playerSettings.update(t) }

    LazyColumn(
        Modifier.fillMaxSize(),
        contentPadding = PaddingValues(start = 48.dp, top = TopBarHeight + 16.dp, bottom = 48.dp, end = 48.dp),
        verticalArrangement = Arrangement.spacedBy(14.dp),
    ) {
        item { Text("Server", style = MaterialTheme.typography.headlineSmall) }
        item {
            Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                Text("${session.info.name}  ·  ${session.baseUrl.removePrefix("http://").trimEnd('/')}", style = MaterialTheme.typography.bodyLarge)
                Text("Server version ${session.info.version} on ${session.info.os}", style = MaterialTheme.typography.bodyMedium, color = Muted)
            }
        }
        item { ActionButton("Change server", onClick = onChangeServer, modifier = Modifier.focusRequester(entry)) }

        item { Text("Subtitles", style = MaterialTheme.typography.headlineSmall, modifier = Modifier.padding(top = 18.dp)) }
        item {
            Choice("Size", PlayerSettings.sizes, s.subtitleSize, { it }) { v -> update { it.copy(subtitleSize = v) } }
        }
        item {
            Choice("Background", PlayerSettings.backgrounds, s.subtitleBackground,
                { mapOf("none" to "None", "shadow" to "Shadow", "box" to "Box")[it] ?: it }) { v -> update { it.copy(subtitleBackground = v) } }
        }
        item {
            Choice("Position", PlayerSettings.positions, s.subtitlePosition,
                { mapOf("bottom" to "Bottom", "raised" to "Raised")[it] ?: it }) { v -> update { it.copy(subtitlePosition = v) } }
        }
        item {
            Text("Preferred subtitle and audio languages are set on the server (subtitles.preferred_languages in config.yaml); " +
                "choices you make in the player are remembered per show.",
                style = MaterialTheme.typography.bodySmall, color = Muted, modifier = Modifier.width(720.dp))
        }

        item { Text("Playback", style = MaterialTheme.typography.headlineSmall, modifier = Modifier.padding(top = 18.dp)) }
        item {
            Choice("Autoplay next episode", listOf(true, false), s.autoplayNext, { if (it) "On" else "Off" }) { v ->
                update { it.copy(autoplayNext = v) }
            }
        }
        item {
            Choice("Ask \"Are you still watching?\"", listOf(true, false), s.stillWatching, { if (it) "On" else "Off" }) { v ->
                update { it.copy(stillWatching = v) }
            }
        }

        item { Text("About", style = MaterialTheme.typography.headlineSmall, modifier = Modifier.padding(top = 18.dp)) }
        item { Text("MiniMedia for Google TV ${BuildConfig.VERSION_NAME}", style = MaterialTheme.typography.bodyMedium, color = Muted) }
    }
}

/** A label and a row of options; the current one is marked. */
@Composable
private fun <T> Choice(label: String, options: List<T>, current: T, name: (T) -> String, onPick: (T) -> Unit) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Text(label, style = MaterialTheme.typography.titleMedium, modifier = Modifier.width(280.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            options.forEach { o ->
                ActionButton(if (o == current) "● ${name(o)}" else name(o), onClick = { onPick(o) })
            }
        }
    }
}
