package io.github.acip.minimedia.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusProperties
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Tab
import androidx.tv.material3.TabRow
import androidx.tv.material3.Text
import io.github.acip.minimedia.data.Media
import io.github.acip.minimedia.data.Session
import kotlinx.coroutines.delay

private val sections = listOf("Home", "Movies", "Shows", "Search", "Settings")

/** Top navigation (PLAN.md 8.3) over the selected section. Focusing a tab selects it. */
@Composable
fun MainScreen(
    session: Session,
    onOpen: (Media) -> Unit,
    onPlay: (String, String) -> Unit,
    onChangeServer: () -> Unit,
) {
    var selected by rememberSaveable { mutableIntStateOf(0) }
    // Sections built so far; they stay built.
    val built = remember { mutableStateListOf(selected) }
    fun select(i: Int) {
        selected = i
        if (i !in built) built.add(i)
    }
    val screenActive = LocalScreenActive.current
    val restorer = LocalFocusRestorer.current
    val tabFocus = remember { sections.map { FocusRequester() } }
    // Settings content is left-aligned, far from its tab: point Down at it explicitly.
    val settingsEntry = remember { FocusRequester() }

    // Entering (or coming back to) this screen: focus what the section asks
    // for, else the selected tab (never the first tab by accident).
    LaunchedEffect(Unit) {
        delay(50)
        runCatching { tabFocus[selected].requestFocus() }
        // The section may still be loading; hand focus over once its target exists.
        repeat(15) {
            restorer.target?.let { t -> if (runCatching { t.requestFocus() }.isSuccess) return@LaunchedEffect }
            delay(100)
        }
    }

    CompositionLocalProvider(LocalFocusRestorer provides restorer) {
        Box(Modifier.fillMaxSize()) {
            // Sections stay built once visited (and Movies and Shows are built
            // ahead, once Home is idle), so switching tabs is instant.
            LaunchedEffect(Unit) {
                delay(1500)
                listOf(1, 2).forEach { if (it !in built) built.add(it) }
            }
            KeepAlive(built.sorted(), selected) { i ->
                CompositionLocalProvider(LocalScreenActive provides (screenActive && i == selected)) {
                    when (i) {
                        0 -> HomeScreen(session, onOpen, onPlay)
                        1 -> GridScreen(session, shows = false, onOpen = onOpen)
                        2 -> GridScreen(session, shows = true, onOpen = onOpen)
                        3 -> SearchScreen(session, onOpen = onOpen)
                        4 -> SettingsScreen(session, onChangeServer = onChangeServer, entry = settingsEntry)
                    }
                }
            }
            Row(
                Modifier
                    .fillMaxWidth()
                    // Content scrolling under the navigation fades out instead of overlapping it.
                    .then(if (selected == 0) Modifier else Modifier.verticalFade(0f to 1f, 0.8f to 1f, 1f to 0f))
                    .padding(start = 48.dp, top = 20.dp, end = 48.dp, bottom = 12.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text("MiniMedia", style = MaterialTheme.typography.titleLarge, color = Accent)
                Spacer(Modifier.width(32.dp))
                TabRow(selectedTabIndex = selected) {
                    sections.forEachIndexed { i, title ->
                        Tab(
                            selected = i == selected,
                            onFocus = {
                                if (selected != i) restorer.target = null
                                select(i)
                            },
                            onClick = { select(i) },
                            modifier = Modifier.focusRequester(tabFocus[i])
                                .focusProperties { if (selected == 4) down = settingsEntry },
                        ) {
                            Text(title, style = MaterialTheme.typography.titleSmall, modifier = Modifier.padding(horizontal = 16.dp, vertical = 6.dp))
                        }
                    }
                }
            }
        }
    }
}

/** Height reserved at the top of each section for the navigation row. */
val TopBarHeight = 72.dp
