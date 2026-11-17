package io.github.acip.minimedia.ui

import android.app.Application
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import coil3.compose.AsyncImage
import io.github.acip.minimedia.data.HomeResponse
import io.github.acip.minimedia.data.Media
import io.github.acip.minimedia.data.Session
import io.github.acip.minimedia.graph
import io.github.acip.minimedia.util.endsAt
import io.github.acip.minimedia.util.episodeCode
import io.github.acip.minimedia.util.formatRuntime
import io.github.acip.minimedia.util.remainingMs
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

class HomeViewModel(app: Application) : AndroidViewModel(app) {
    private val graph = app.graph
    var home by mutableStateOf<HomeResponse?>(null)
        private set
    var error by mutableStateOf<String?>(null)
        private set
    /** Focus goes to the hero's Play button only the first time, not on every tab switch. */
    var initialFocusDone = false
    /** "row/id" of the card the user opened, or "hero", to focus again on the way back. */
    var lastOpened by mutableStateOf<String?>(null)

    fun load() = viewModelScope.launch {
        runCatching { graph.requireSession().api.home() }
            .onSuccess { home = it; error = null }
            .onFailure { if (home == null) error = "Could not load the library: ${it.message}" }
    }
}

@Composable
fun HomeScreen(session: Session, onOpen: (Media) -> Unit, onPlay: (String, String) -> Unit, vm: HomeViewModel = viewModel()) {
    // Reload whenever the screen comes back (e.g. after playback) so progress is current.
    OnScreenShown { vm.load() }
    val home = vm.home
    if (home == null) {
        LoadingOrError(vm.error)
        return
    }
    if (home.hero.isEmpty() && home.movies.isEmpty() && home.shows.isEmpty()) {
        CenteredMessage("Your library is empty. Add folders under 'libraries' in the server's config.yaml.")
        return
    }

    var focused by remember { mutableStateOf<Media?>(null) }
    var heroIndex by remember { mutableIntStateOf(0) }
    var rowsHaveFocus by remember { mutableStateOf(false) }
    var heroHasFocus by remember { mutableStateOf(false) }
    // Rotate the featured title every 10 s while the user isn't on the hero or the rows.
    val active = LocalScreenActive.current
    LaunchedEffect(rowsHaveFocus, heroHasFocus, home.hero.size, active) {
        while (active && !rowsHaveFocus && !heroHasFocus && home.hero.size > 1) {
            delay(10_000)
            heroIndex = (heroIndex + 1) % home.hero.size
        }
    }
    val heroItem = (if (rowsHaveFocus) focused else null) ?: home.hero.getOrNull(heroIndex) ?: focused
    // Work ahead while focus rests (PLAN: "instant" actions): a card that
    // plays gets its video buffered, one that opens a page gets its details
    // and artwork fetched; the hero likewise, after a longer rest.
    val graph = LocalContext.current.graph
    var restingOn by remember { mutableStateOf<Pair<Media, Boolean>?>(null) }
    LaunchedEffect(restingOn) {
        val (m, plays) = restingOn ?: return@LaunchedEffect
        delay(if (plays) 800 else 300)
        if (plays) graph.preloader.preload(m.id) else graph.details.prefetch(m)
    }
    LaunchedEffect(heroHasFocus, heroItem?.id) {
        val m = heroItem ?: return@LaunchedEffect
        if (!heroHasFocus) return@LaunchedEffect
        graph.details.prefetch(m)
        delay(1500)
        if (!m.isShow) graph.preloader.preload(m.id)
    }
    val playFocus = remember { FocusRequester() }
    val reopen = remember { FocusRequester() }
    val restorer = LocalFocusRestorer.current
    val shown = LocalScreenActive.current
    SideEffect {
        if (shown) when {
            !vm.initialFocusDone -> { vm.initialFocusDone = true; restorer.target = playFocus }
            vm.lastOpened == "hero" -> restorer.target = playFocus
            vm.lastOpened != null -> restorer.target = reopen
        }
    }

    // The backdrop follows focus only once it rests on a card: loading and
    // cross-fading a full-screen image on every key press of a fast scroll
    // would make the rows stutter. The text above the rows updates at once.
    val backdropTarget = session.image(heroItem?.backdropUrl, 1280)
    var backdrop by remember { mutableStateOf(backdropTarget) }
    LaunchedEffect(backdropTarget) {
        if (backdrop != null) delay(350)
        backdrop = backdropTarget
    }

    Box(Modifier.fillMaxSize()) {
        Backdrop(backdrop)
        Column(Modifier.fillMaxSize()) {
            Spacer(Modifier.height(TopBarHeight))
            if (heroItem != null) {
                HeroInfo(
                    m = heroItem,
                    session = session,
                    compact = rowsHaveFocus,
                    playFocus = playFocus,
                    onPlay = { m ->
                        vm.lastOpened = "hero"
                        restorer.target = playFocus
                        if (m.isShow) onOpen(m) else onPlay(m.id, StartMode.ASK)
                    },
                    onMore = { vm.lastOpened = "hero"; restorer.target = playFocus; onOpen(it) },
                    modifier = Modifier.onFocusChanged { heroHasFocus = it.hasFocus },
                )
            }
            LazyColumn(
                Modifier.fillMaxWidth().weight(1f).onFocusChanged { rowsHaveFocus = it.hasFocus },
                contentPadding = PaddingValues(bottom = 48.dp),
                verticalArrangement = Arrangement.spacedBy(28.dp),
            ) {
                val rows = listOf(
                    Triple("Continue Watching", home.continueWatching, true),
                    Triple("Next Up", home.nextUp, true),
                    Triple("Recently Added Movies", home.recentlyAddedMovies, false),
                    Triple("Recently Added Episodes", home.recentlyAddedEpisodes, true),
                    Triple("Movies", home.movies, false),
                    Triple("Shows", home.shows, false),
                )
                rows.filter { it.second.isNotEmpty() }.forEach { (title, items, landscape) ->
                    item(key = title) {
                        MediaRow(
                            title, items, session, landscape,
                            onClick = { m ->
                                vm.lastOpened = "$title/${m.id}"
                                restorer.target = reopen
                                if (m.isShow || (!landscape && m.kind == "movie")) onOpen(m) else onPlay(m.id, StartMode.ASK)
                            },
                            onFocus = { focused = it; restingOn = it to !(it.isShow || (!landscape && it.kind == "movie")) },
                            itemModifier = { m -> if (vm.lastOpened == "$title/${m.id}") Modifier.focusRequester(reopen) else Modifier },
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun HeroInfo(
    m: Media,
    session: Session,
    compact: Boolean,
    playFocus: FocusRequester,
    onPlay: (Media) -> Unit,
    onMore: (Media) -> Unit,
    modifier: Modifier = Modifier,
) {
    val ctx = LocalContext.current
    Column(modifier.padding(start = 48.dp, end = 48.dp, top = 8.dp).width(620.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        val logo = session.image(m.logoUrl, 780)
        val title = if (m.isEpisode) m.showTitle ?: m.title else m.title
        if (logo != null) {
            AsyncImage(logo, title, Modifier.heightIn(max = if (compact) 70.dp else 110.dp).width(380.dp),
                contentScale = ContentScale.Fit, alignment = androidx.compose.ui.Alignment.CenterStart)
        } else {
            Text(title, style = MaterialTheme.typography.displaySmall, fontWeight = FontWeight.Bold, maxLines = 2, overflow = TextOverflow.Ellipsis)
        }
        val meta = buildList {
            if (m.isEpisode) add(listOf(episodeCode(m), m.title).filter { it.isNotEmpty() }.joinToString(" · "))
            if (m.year > 0) add("${m.year}")
            if (m.isShow) {
                if (m.seasonCount > 0) add(if (m.seasonCount == 1) "1 season" else "${m.seasonCount} seasons")
            } else if (m.runtimeMs > 0) {
                add(formatRuntime(m.runtimeMs))
                add("Ends at ${endsAt(ctx, remainingMs(m))}")
            }
            if (m.genres.isNotEmpty()) add(m.genres.take(3).joinToString(", "))
        }
        Text(meta.filter { it.isNotBlank() }.joinToString("  ·  "), style = MaterialTheme.typography.bodyLarge, color = Muted)
        if (!compact) {
            if (m.overview.isNotEmpty()) {
                Text(m.overview, style = MaterialTheme.typography.bodyMedium, maxLines = 3, overflow = TextOverflow.Ellipsis)
            }
            Row(horizontalArrangement = Arrangement.spacedBy(12.dp), modifier = Modifier.padding(top = 6.dp, bottom = 20.dp)) {
                val resume = m.positionMs > 0 && !m.watched
                ActionButton(
                    if (m.isShow) "Episodes" else if (resume) "Resume" else "Play",
                    onClick = { onPlay(m) },
                    icon = AppIcons.Play,
                    modifier = Modifier.focusRequester(playFocus),
                )
                ActionButton("More Info", onClick = { onMore(m) })
            }
        } else {
            Spacer(Modifier.height(12.dp))
        }
    }
}
