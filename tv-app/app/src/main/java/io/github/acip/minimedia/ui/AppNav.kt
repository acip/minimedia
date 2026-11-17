package io.github.acip.minimedia.ui

import android.app.Application
import androidx.activity.compose.BackHandler
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalContext
import io.github.acip.minimedia.data.Media
import io.github.acip.minimedia.data.Session
import io.github.acip.minimedia.graph
import io.github.acip.minimedia.ui.player.PlayerScreen

/** How the player starts: ask about resuming, resume, or from the beginning. */
object StartMode {
    const val ASK = "ask"
    const val RESUME = "resume"
    const val BEGINNING = "beginning"
}

fun detailsRoute(m: Media): Route = when {
    m.isShow -> Route.Show(m.id, seed = m)
    m.isEpisode && m.showId != null -> Route.Show(m.showId)
    else -> Route.Movie(m.id, seed = m)
}

@Composable
fun AppNav() {
    val app = LocalContext.current.applicationContext as Application
    val nav = remember { Navigator(app).apply { push(Route.Connect(forget = false)) } }
    val open: (Media) -> Unit = { nav.push(detailsRoute(it)) }
    val play: (String, String) -> Unit = { id, start -> nav.push(Route.Player(id, start)) }

    // Registered first, so screens' own Back handling (player panels) wins.
    BackHandler(enabled = nav.stack.size > 1) { nav.pop() }

    ScreenStack(nav) { entry ->
        when (val r = entry.route) {
            is Route.Connect -> ConnectScreen(
                forget = r.forget,
                onConnected = { nav.reset(Route.Main) },
            )
            Route.Main -> WithSession(nav) { s ->
                MainScreen(
                    session = s,
                    onOpen = open,
                    onPlay = play,
                    onChangeServer = { nav.reset(Route.Connect(forget = true)) },
                )
            }
            is Route.Movie -> WithSession(nav) { s -> MovieDetailsScreen(r.id, r.seed, s, onPlay = play) }
            is Route.Show -> WithSession(nav) { s -> ShowDetailsScreen(r.id, r.seed, s, onPlay = play) }
            is Route.Player -> WithSession(nav) { s ->
                PlayerScreen(
                    itemId = r.id,
                    startMode = r.start,
                    session = s,
                    onExit = { nav.pop() },
                    onPlayNext = { next, mode -> nav.replaceTop(Route.Player(next, mode)) },
                )
            }
        }
    }
}

/** Screens below "connect" need a session; after process death there is none, so reconnect. */
@Composable
private fun WithSession(nav: Navigator, content: @Composable (Session) -> Unit) {
    val session = LocalContext.current.graph.session
    if (session == null) {
        LaunchedEffect(Unit) { nav.reset(Route.Connect(forget = false)) }
        return
    }
    content(session)
}
