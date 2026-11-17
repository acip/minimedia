package io.github.acip.minimedia.ui

import android.app.Application
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.tv.material3.ClickableSurfaceDefaults
import androidx.tv.material3.Icon
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Surface
import androidx.tv.material3.Text
import coil3.compose.AsyncImage
import io.github.acip.minimedia.data.ItemDetail
import io.github.acip.minimedia.data.Media
import io.github.acip.minimedia.data.Session
import io.github.acip.minimedia.data.ShowDetail
import io.github.acip.minimedia.graph
import io.github.acip.minimedia.util.endsAt
import io.github.acip.minimedia.util.episodeCode
import io.github.acip.minimedia.util.formatClock
import io.github.acip.minimedia.util.formatRuntime
import io.github.acip.minimedia.util.progressFraction
import io.github.acip.minimedia.util.remainingMs
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

// ---- movie -------------------------------------------------------------------

class MovieDetailsViewModel(app: Application) : AndroidViewModel(app) {
    private val graph = app.graph
    var detail by mutableStateOf<ItemDetail?>(null)
        private set
    var unsupported by mutableStateOf<String?>(null)
        private set
    var error by mutableStateOf<String?>(null)
        private set

    /** Shows what is already known (prefetched details, or the card's data) until load() returns. */
    fun prime(provisional: ItemDetail?) {
        if (detail == null) detail = provisional
    }

    fun load(id: String) = viewModelScope.launch {
        // Starts buffering the video too (Preloader): Play is then near-instant.
        // The decision also tells whether this device can play the video at all.
        val decision = graph.preloader.preload(id)
        val api = graph.requireSession().api
        runCatching { api.item(id) }
            .onSuccess { detail = it; error = null; graph.details.put(it) }
            .onFailure { if (detail == null) error = "Could not load: ${it.message}" }
        runCatching { decision.await() }.getOrNull()?.let { unsupported = it.unsupportedReason }
    }

    fun toggleWatched(id: String) = viewModelScope.launch {
        val api = graph.requireSession().api
        val d = detail ?: return@launch
        runCatching { if (d.item.watched) api.markUnwatched(id) else api.markWatched(id) }
        load(id)
    }
}

@Composable
fun MovieDetailsScreen(id: String, seed: Media?, session: Session, onPlay: (String, String) -> Unit) {
    val vm: MovieDetailsViewModel = viewModel(key = "movie-$id")
    val graph = LocalContext.current.graph
    remember(id) {
        vm.prime(graph.details.item(id) ?: seed?.let { ItemDetail(item = it, overview = it.overview, genres = it.genres, durationMs = it.runtimeMs) })
    }
    OnScreenShown { vm.load(id) }
    val d = vm.detail
    if (d == null) {
        LoadingOrError(vm.error)
        return
    }
    val m = d.item
    val ctx = LocalContext.current
    val playFocus = remember { FocusRequester() }

    Box(Modifier.fillMaxSize()) {
        Backdrop(session.image(m.backdropUrl, 1280))
        Column(
            Modifier.padding(start = 48.dp, top = 48.dp, end = 48.dp).width(600.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            TitleOrLogo(m.title, session.image(m.logoUrl, 780))
            val meta = buildList {
                if (m.year > 0) add("${m.year}")
                if (d.durationMs > 0) add(formatRuntime(d.durationMs))
                if (d.durationMs > 0) add("Ends at ${endsAt(ctx, remainingMs(m.copy(runtimeMs = d.durationMs)))}")
                d.rating?.let { add("★ %.1f".format(it)) }
            }
            Text(meta.filter { it.isNotBlank() }.joinToString("  ·  "), style = MaterialTheme.typography.bodyLarge, color = Muted)
            if (d.genres.isNotEmpty()) Text(d.genres.joinToString(", "), style = MaterialTheme.typography.bodyMedium, color = Muted)
            progressFraction(m.copy(runtimeMs = d.durationMs))?.let { f ->
                Row(verticalAlignment = Alignment.CenterVertically) {
                    ProgressBar(f, Modifier.width(220.dp))
                    Spacer(Modifier.width(12.dp))
                    Text("${formatClock(d.durationMs - m.positionMs)} left", style = MaterialTheme.typography.bodySmall, color = Muted)
                }
            }
            if (d.overview.isNotEmpty()) {
                Text(d.overview, style = MaterialTheme.typography.bodyMedium, maxLines = 5, overflow = TextOverflow.Ellipsis)
            }
            vm.unsupported?.let { Text(it, style = MaterialTheme.typography.bodyLarge, color = Color(0xFFFF8A80)) }
            Row(horizontalArrangement = Arrangement.spacedBy(12.dp), modifier = Modifier.padding(top = 8.dp)) {
                val canPlay = vm.unsupported == null
                val resume = m.positionMs > 0 && !m.watched
                if (resume) {
                    ActionButton("Resume from ${formatClock(m.positionMs)}", onClick = { onPlay(m.id, StartMode.RESUME) },
                        icon = AppIcons.Play, enabled = canPlay, modifier = Modifier.focusRequester(playFocus))
                    ActionButton("Play from beginning", onClick = { onPlay(m.id, StartMode.BEGINNING) }, icon = AppIcons.Restart, enabled = canPlay)
                } else {
                    ActionButton("Play", onClick = { onPlay(m.id, StartMode.BEGINNING) }, icon = AppIcons.Play,
                        enabled = canPlay, modifier = Modifier.focusRequester(playFocus))
                }
                ActionButton(if (m.watched) "Mark unwatched" else "Mark watched", onClick = { vm.toggleWatched(m.id) }, icon = AppIcons.Check)
            }
        }
    }
    val restorer = LocalFocusRestorer.current
    SideEffect { restorer.target = playFocus }
    LaunchedEffect(Unit) { runCatching { playFocus.requestFocus() } }
}

@Composable
private fun TitleOrLogo(title: String, logo: String?) {
    if (logo != null) {
        AsyncImage(logo, title, Modifier.heightIn(max = 110.dp).width(400.dp), contentScale = ContentScale.Fit, alignment = Alignment.CenterStart)
    } else {
        Text(title, style = MaterialTheme.typography.displaySmall, fontWeight = FontWeight.Bold, maxLines = 2, overflow = TextOverflow.Ellipsis)
    }
}

// ---- show --------------------------------------------------------------------

class ShowDetailsViewModel(app: Application) : AndroidViewModel(app) {
    private val graph = app.graph
    var detail by mutableStateOf<ShowDetail?>(null)
        private set
    var error by mutableStateOf<String?>(null)
        private set
    var season by mutableIntStateOf(-1) // index into detail.seasons
    /** The episode last played; focused again when coming back from the player. */
    var lastPlayedId: String? = null

    /** Episodes have been focused once (on arrival); later loads keep focus where it is. */
    var focusedOnce = false

    fun prime(provisional: ShowDetail?) {
        if (detail == null && provisional != null) applyDetail(provisional)
    }

    private fun applyDetail(d: ShowDetail) {
        val hadSeasons = detail?.seasons?.isNotEmpty() == true
        detail = d
        if (season !in d.seasons.indices || !hadSeasons) {
            // Open the season holding the next-up episode.
            season = d.seasons.indexOfFirst { s -> s.episodes.any { it.id == d.nextUpId } }.coerceAtLeast(0)
        }
    }

    fun load(id: String) = viewModelScope.launch {
        runCatching { graph.requireSession().api.show(id) }
            .onSuccess { d ->
                applyDetail(d)
                graph.details.put(d)
                error = null
            }
            .onFailure { if (detail == null) error = "Could not load: ${it.message}" }
    }
}

@Composable
fun ShowDetailsScreen(id: String, seed: Media?, session: Session, onPlay: (String, String) -> Unit) {
    val vm: ShowDetailsViewModel = viewModel(key = "show-$id")
    val graph = LocalContext.current.graph
    remember(id) { vm.prime(graph.details.show(id) ?: seed?.takeIf { it.isShow }?.let { ShowDetail(show = it, overview = it.overview) }) }
    OnScreenShown { vm.load(id) }
    val d = vm.detail
    if (d == null) {
        LoadingOrError(vm.error)
        return
    }
    val show = d.show
    val seasonIdx = vm.season.coerceIn(0, (d.seasons.size - 1).coerceAtLeast(0))
    val episodes = d.seasons.getOrNull(seasonIdx)?.episodes.orEmpty()
    val listState = rememberLazyListState()
    val episodeFocus = remember(seasonIdx, episodes.size) { episodes.map { FocusRequester() } }
    val restorer = LocalFocusRestorer.current
    // Buffer the episode the user rests on, so OK starts it at once.
    var focusedEpisode by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(focusedEpisode) {
        val id = focusedEpisode ?: return@LaunchedEffect
        delay(800)
        graph.preloader.preload(id)
    }

    Box(Modifier.fillMaxSize()) {
        Backdrop(session.image(show.backdropUrl, 1280))
        Column(Modifier.fillMaxSize().padding(top = 36.dp)) {
            Column(Modifier.padding(horizontal = 48.dp).width(620.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                TitleOrLogo(show.title, session.image(show.logoUrl, 780))
                val meta = buildList {
                    if (show.year > 0) add("${show.year}")
                    if (show.seasonCount > 0) add(if (show.seasonCount == 1) "1 season" else "${show.seasonCount} seasons")
                    if (show.genres.isNotEmpty()) add(show.genres.take(3).joinToString(", "))
                    d.rating?.let { add("★ %.1f".format(it)) }
                }
                Text(meta.filter { it.isNotBlank() }.joinToString("  ·  "), style = MaterialTheme.typography.bodyLarge, color = Muted)
                if (d.overview.isNotEmpty()) {
                    Text(d.overview, style = MaterialTheme.typography.bodyMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
                }
            }
            if (d.seasons.size > 1) {
                LazyRow(
                    contentPadding = PaddingValues(horizontal = 48.dp, vertical = 12.dp),
                    horizontalArrangement = Arrangement.spacedBy(10.dp),
                ) {
                    itemsIndexed(d.seasons) { i, s ->
                        ActionButton(
                            if (i == seasonIdx) "● ${s.title}" else s.title,
                            onClick = { vm.season = i },
                            modifier = Modifier.onFocusChanged { if (it.isFocused) vm.season = i },
                        )
                    }
                }
            } else {
                Spacer(Modifier.height(12.dp))
            }
            LazyColumn(
                state = listState,
                modifier = Modifier.fillMaxWidth().weight(1f),
                contentPadding = PaddingValues(start = 48.dp, end = 48.dp, top = 4.dp, bottom = 48.dp),
                verticalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                itemsIndexed(episodes, key = { _, e -> e.id }) { i, e ->
                    EpisodeRow(e, session, onClick = { vm.lastPlayedId = e.id; restorer.target = episodeFocus[i]; onPlay(e.id, StartMode.ASK) },
                        modifier = Modifier.focusRequester(episodeFocus[i]).onFocusChanged { if (it.isFocused) focusedEpisode = e.id })
                }
            }
        }
    }
    // Focus the next-up episode on arrival (PLAN.md 8.3), and the episode just
    // played when coming back from the player.
    LaunchedEffect(episodes.isNotEmpty()) {
        if (episodes.isEmpty() || vm.focusedOnce) return@LaunchedEffect
        vm.focusedOnce = true
        val target = vm.lastPlayedId ?: d.nextUpId
        val i = episodes.indexOfFirst { it.id == target }.coerceAtLeast(0)
        listState.scrollToItem(i)
        delay(80)
        runCatching { episodeFocus[i].requestFocus() }
    }
}

@Composable
private fun EpisodeRow(e: Media, session: Session, onClick: () -> Unit, modifier: Modifier = Modifier) {
    val ctx = LocalContext.current
    Surface(
        onClick = onClick,
        modifier = modifier.fillMaxWidth(),
        shape = ClickableSurfaceDefaults.shape(RoundedCornerShape(10.dp)),
        colors = ClickableSurfaceDefaults.colors(
            containerColor = Color(0x66000000),
            focusedContainerColor = Color(0x33FFFFFF),
        ),
        scale = ClickableSurfaceDefaults.scale(focusedScale = 1.01f),
    ) {
        Row(Modifier.padding(10.dp), verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.width(200.dp).aspectRatio(16f / 9f).clip(RoundedCornerShape(6.dp))) {
                val still = session.image(e.stillUrl, 342)
                if (still != null) AsyncImage(still, e.title, Modifier.fillMaxSize(), contentScale = ContentScale.Crop)
                else Box(Modifier.fillMaxSize().padding(1.dp)) { }
                progressFraction(e)?.let { ProgressBar(it, Modifier.align(Alignment.BottomCenter).fillMaxWidth().padding(6.dp)) }
            }
            Spacer(Modifier.width(18.dp))
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(listOf(episodeCode(e), e.title).filter { it.isNotEmpty() }.joinToString("  "),
                        style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.weight(1f, fill = false))
                    if (e.watched) {
                        Spacer(Modifier.width(10.dp))
                        Icon(AppIcons.Check, "Watched", Modifier.size(18.dp), tint = Accent)
                    }
                }
                if (e.runtimeMs > 0) {
                    Text(listOf(formatRuntime(e.runtimeMs), "Ends at ${endsAt(ctx, remainingMs(e))}").filter { it.isNotBlank() }.joinToString("  ·  "),
                        style = MaterialTheme.typography.bodySmall, color = Muted)
                }
                if (e.overview.isNotEmpty()) {
                    Text(e.overview, style = MaterialTheme.typography.bodySmall, maxLines = 2, overflow = TextOverflow.Ellipsis)
                }
            }
        }
    }
}
