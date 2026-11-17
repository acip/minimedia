package io.github.acip.minimedia.ui

import android.app.Application
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.unit.dp
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import io.github.acip.minimedia.data.Media
import io.github.acip.minimedia.data.Session
import io.github.acip.minimedia.graph
import androidx.compose.ui.platform.LocalContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

class GridViewModel(app: Application) : AndroidViewModel(app) {
    private val graph = app.graph
    var items by mutableStateOf<List<Media>?>(null)
        private set
    var genres by mutableStateOf<List<String>>(emptyList())
        private set
    var sort by mutableStateOf("title")
        private set
    var genre by mutableStateOf<String?>(null)
        private set
    var error by mutableStateOf<String?>(null)
        private set
    private var shows = false
    /** The card the user opened, to focus again on the way back. */
    var lastOpenedId by mutableStateOf<String?>(null)

    fun init(shows: Boolean) {
        this.shows = shows
        load() // also on return, so watched marks are current
    }

    fun changeSort(s: String) { sort = s; load() }

    /** Cycles All → each genre → All. */
    fun nextGenre() {
        val i = genres.indexOf(genre)
        genre = if (i + 1 < genres.size) genres[i + 1] else null
        load()
    }

    private fun load() = viewModelScope.launch {
        val api = graph.requireSession().api
        runCatching { if (shows) api.shows(sort, genre) else api.movies(sort, genre) }
            .onSuccess { list ->
                items = list
                error = null
                if (genre == null) genres = list.flatMap { it.genres }.distinct().sorted()
            }
            .onFailure { error = "Could not load: ${it.message}" }
    }
}

private val sorts = listOf("title" to "A–Z", "added" to "Recently added", "year" to "Year")

/** Movies or Shows: a poster grid with sort and genre filter (PLAN.md 8.3). */
@Composable
fun GridScreen(session: Session, shows: Boolean, onOpen: (Media) -> Unit) {
    val vm: GridViewModel = viewModel(key = if (shows) "grid-shows" else "grid-movies")
    // Loaded when built (possibly ahead, while hidden) and again whenever
    // shown, so watched marks are current after coming back.
    LaunchedEffect(shows) { vm.init(shows) }
    OnScreenShown { vm.init(shows) }
    val restorer = LocalFocusRestorer.current
    val reopen = remember { FocusRequester() }
    // Fetch the details of the card focus rests on, so opening it is instant.
    val graph = LocalContext.current.graph
    var resting by remember { mutableStateOf<Media?>(null) }
    LaunchedEffect(resting) {
        val m = resting ?: return@LaunchedEffect
        delay(300)
        graph.details.prefetch(m)
    }

    Column(Modifier.fillMaxSize().padding(top = TopBarHeight + 8.dp)) {
        Row(
            Modifier.padding(horizontal = 48.dp),
            horizontalArrangement = Arrangement.spacedBy(12.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(if (shows) "Shows" else "Movies", style = MaterialTheme.typography.headlineMedium)
            Spacer(Modifier.width(16.dp))
            sorts.forEach { (key, label) ->
                ActionButton(if (vm.sort == key) "● $label" else label, onClick = { vm.changeSort(key) })
            }
            if (vm.genres.isNotEmpty()) {
                ActionButton("Genre: ${vm.genre ?: "All"}", onClick = vm::nextGenre)
            }
        }
        Spacer(Modifier.height(16.dp))
        val items = vm.items
        when {
            items == null -> LoadingOrError(vm.error)
            items.isEmpty() -> CenteredMessage(if (vm.genre != null) "Nothing in ${vm.genre}" else "Nothing here yet")
            else -> LazyVerticalGrid(
                columns = GridCells.Adaptive(150.dp),
                contentPadding = PaddingValues(start = 48.dp, end = 48.dp, top = 12.dp, bottom = 48.dp),
                horizontalArrangement = Arrangement.spacedBy(20.dp),
                verticalArrangement = Arrangement.spacedBy(24.dp),
            ) {
                items(items, key = { it.id }) { m ->
                    val last = m.id == vm.lastOpenedId
                    if (last && LocalScreenActive.current) SideEffect { restorer.target = reopen }
                    PosterCard(
                        m, session,
                        onFocus = { resting = m },
                        onClick = { vm.lastOpenedId = m.id; restorer.target = reopen; onOpen(m) },
                        modifier = if (last) Modifier.focusRequester(reopen) else Modifier,
                    )
                }
            }
        }
    }
}
