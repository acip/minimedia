package io.github.acip.minimedia.ui

import android.app.Application
import androidx.compose.foundation.focusGroup
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.focusRestorer
import androidx.compose.ui.layout.Layout
import androidx.compose.ui.platform.LocalFocusManager
import androidx.lifecycle.HasDefaultViewModelProviderFactory
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.ViewModelStore
import androidx.lifecycle.ViewModelStoreOwner
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import androidx.lifecycle.viewmodel.CreationExtras
import androidx.lifecycle.viewmodel.MutableCreationExtras
import androidx.lifecycle.viewmodel.compose.LocalViewModelStoreOwner
import io.github.acip.minimedia.data.Media
import kotlinx.coroutines.android.awaitFrame

/*
 * Screens stay alive underneath the one on top, like a native TV app's view
 * stack: Back reveals a screen that is already built (and measured), with
 * focus restored to the card that was opened, instead of rebuilding it (one
 * 100–200 ms frame on a Chromecast). Hidden screens are not placed, so they
 * cost nothing to draw and can't take focus.
 */

sealed interface Route {
    data class Connect(val forget: Boolean) : Route
    data object Main : Route
    /** seed: what the card already knows, shown until the full details arrive. */
    data class Movie(val id: String, val seed: Media? = null) : Route
    data class Show(val id: String, val seed: Media? = null) : Route
    data class Player(val id: String, val start: String) : Route
}

/** One screen on the stack, with its own ViewModels (cleared when it is removed). */
class StackEntry(val route: Route, app: Application) : ViewModelStoreOwner, HasDefaultViewModelProviderFactory {
    override val viewModelStore = ViewModelStore()
    override val defaultViewModelProviderFactory: ViewModelProvider.Factory =
        ViewModelProvider.AndroidViewModelFactory.getInstance(app)
    override val defaultViewModelCreationExtras: CreationExtras =
        MutableCreationExtras().apply { set(ViewModelProvider.AndroidViewModelFactory.APPLICATION_KEY, app) }
    val focus = FocusRequester()
    /** What the screen wants focused when revealed again (the card or button that left it). */
    val restorer = FocusRestorer()
}

class Navigator(private val app: Application) {
    val stack = mutableStateListOf<StackEntry>()

    val top: StackEntry? get() = stack.lastOrNull()

    fun push(route: Route) {
        stack.add(StackEntry(route, app))
    }

    fun pop(): Boolean {
        if (stack.size <= 1) return false
        stack.removeAt(stack.lastIndex)
        return true
    }

    /** Replaces the top screen (e.g. the player moving on to the next episode). */
    fun replaceTop(route: Route) {
        if (stack.isNotEmpty()) stack.removeAt(stack.lastIndex)
        push(route)
    }

    /** Starts over with a single screen. */
    fun reset(route: Route) {
        stack.clear()
        push(route)
    }
}

/** Whether the screen (and, inside Main, the section) is the one being shown. */
val LocalScreenActive = compositionLocalOf { true }

/**
 * Runs block whenever this screen is shown: when it first appears, when it is
 * revealed again by Back, and when the app comes back to the foreground.
 * Replaces ON_RESUME, which hidden screens no longer get.
 */
@Composable
fun OnScreenShown(block: () -> Unit) {
    val active = LocalScreenActive.current
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    val current by rememberUpdatedState(block)
    LaunchedEffect(active) {
        if (active) lifecycle.repeatOnLifecycle(Lifecycle.State.RESUMED) { current() }
    }
}

/**
 * Composes and measures every item but places only shown, so switching to
 * an item that was already built is just a placement.
 */
@Composable
fun <T : Any> KeepAlive(
    items: List<T>,
    shown: T,
    modifier: Modifier = Modifier,
    itemModifier: (T) -> Modifier = { Modifier },
    content: @Composable (T) -> Unit,
) {
    Layout(
        modifier = modifier,
        content = {
            items.forEach { item ->
                key(item) { Box(itemModifier(item).fillMaxSize()) { content(item) } }
            }
        },
    ) { measurables, constraints ->
        val placeables = measurables.map { it.measure(constraints) }
        val i = items.indexOf(shown)
        layout(constraints.maxWidth, constraints.maxHeight) {
            placeables.getOrNull(i)?.place(0, 0)
        }
    }
}

private class TopHolder(var entry: StackEntry? = null)

@Composable
fun ScreenStack(nav: Navigator, content: @Composable (StackEntry) -> Unit) {
    val top = nav.top ?: return
    val focusManager = LocalFocusManager.current
    val last = remember { TopHolder() }
    // Focus follows the top screen. Covering a screen drops its focus at once
    // (its focusRestorer remembers what had it), so keys pressed before the
    // new screen focuses something can't act on the hidden one. Revealing a
    // screen gives focus back to what it had.
    LaunchedEffect(top) {
        val previous = last.entry
        last.entry = top
        if (previous == null) return@LaunchedEffect
        if (previous in nav.stack) {
            focusManager.clearFocus(force = true)
            return@LaunchedEffect
        }
        repeat(10) {
            awaitFrame()
            val target = top.restorer.target ?: top.focus
            if (runCatching { target.requestFocus() }.getOrDefault(false)) return@LaunchedEffect
        }
        runCatching { top.focus.requestFocus() }
    }
    KeepAlive(
        items = nav.stack.toList(),
        shown = top,
        itemModifier = { e -> Modifier.focusRequester(e.focus).focusRestorer().focusGroup() },
    ) { entry ->
        // A removed screen's ViewModels (e.g. the player's) are cleared once its UI is gone.
        DisposableEffect(entry) {
            onDispose { if (entry !in nav.stack) entry.viewModelStore.clear() }
        }
        CompositionLocalProvider(
            LocalViewModelStoreOwner provides entry,
            LocalFocusRestorer provides entry.restorer,
            LocalScreenActive provides (entry === top),
        ) {
            content(entry)
        }
    }
}
