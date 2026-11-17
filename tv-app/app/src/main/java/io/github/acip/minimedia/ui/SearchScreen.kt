package io.github.acip.minimedia.ui

import android.app.Application
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusDirection
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.tv.material3.Text
import io.github.acip.minimedia.data.Media
import io.github.acip.minimedia.data.SearchResult
import io.github.acip.minimedia.data.Session
import io.github.acip.minimedia.graph
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

class SearchViewModel(app: Application) : AndroidViewModel(app) {
    private val graph = app.graph
    var query by mutableStateOf("")
        private set
    var result by mutableStateOf<SearchResult?>(null)
        private set
    private var job: Job? = null

    fun onQuery(q: String) {
        query = q
        job?.cancel()
        job = viewModelScope.launch {
            delay(300) // debounce typing
            result = if (q.isBlank()) null else runCatching { graph.requireSession().api.search(q.trim()) }.getOrNull()
        }
    }
}

/** Search box (the TV's on-screen keyboard, including voice typing) and result rows. */
@Composable
fun SearchScreen(session: Session, onOpen: (Media) -> Unit) {
    val vm: SearchViewModel = viewModel()
    var fieldFocused by remember { mutableStateOf(false) }
    val focusManager = LocalFocusManager.current

    Column(Modifier.fillMaxSize().padding(top = TopBarHeight + 8.dp)) {
        Box(
            Modifier.padding(horizontal = 48.dp).width(640.dp)
                .border(2.dp, if (fieldFocused) Color.White else Color(0x55FFFFFF), RoundedCornerShape(8.dp))
                .background(Color(0x22FFFFFF), RoundedCornerShape(8.dp))
                .padding(horizontal = 16.dp, vertical = 12.dp),
        ) {
            BasicTextField(
                value = vm.query,
                onValueChange = vm::onQuery,
                singleLine = true,
                textStyle = TextStyle(color = Color.White, fontSize = 22.sp),
                cursorBrush = SolidColor(Color.White),
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
                keyboardActions = KeyboardActions(onSearch = { vm.onQuery(vm.query) }),
                modifier = Modifier
                    .fillMaxWidth()
                    .onFocusChanged { fieldFocused = it.isFocused }
                    // A text field keeps D-pad Up/Down; on a TV they must leave it
                    // (to the tabs above, the results below).
                    .onPreviewKeyEvent { e ->
                        if (e.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                        when (e.nativeKeyEvent.keyCode) {
                            android.view.KeyEvent.KEYCODE_DPAD_UP -> focusManager.moveFocus(FocusDirection.Up)
                            android.view.KeyEvent.KEYCODE_DPAD_DOWN -> focusManager.moveFocus(FocusDirection.Down)
                            else -> false
                        }
                    },
                decorationBox = { inner ->
                    if (vm.query.isEmpty()) Text("Search movies, shows and episodes", color = Color(0x88FFFFFF), fontSize = 22.sp)
                    inner()
                },
            )
        }
        val r = vm.result
        when {
            vm.query.isBlank() -> Unit
            r == null -> CenteredMessage("Searching…")
            r.movies.isEmpty() && r.shows.isEmpty() && r.episodes.isEmpty() ->
                CenteredMessage("Nothing matches \"${vm.query}\"")
            else -> LazyColumn(
                Modifier.fillMaxSize(),
                contentPadding = PaddingValues(top = 24.dp, bottom = 48.dp),
                verticalArrangement = Arrangement.spacedBy(28.dp),
                horizontalAlignment = Alignment.Start,
            ) {
                item { MediaRow("Movies", r.movies, session, landscape = false, onClick = onOpen) }
                item { MediaRow("Shows", r.shows, session, landscape = false, onClick = onOpen) }
                item { MediaRow("Episodes", r.episodes, session, landscape = true, onClick = onOpen) }
            }
        }
    }
}
