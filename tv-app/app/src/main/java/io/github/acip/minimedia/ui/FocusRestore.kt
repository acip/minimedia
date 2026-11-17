package io.github.acip.minimedia.ui

import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.focus.FocusRequester

/**
 * Where focus should go when the main screen comes back (e.g. after Back from
 * a details page). Sections register the card the user opened; without one,
 * the selected tab gets focus. Without this, Compose would focus the first tab
 * on the next key press, and focusing a tab selects it.
 */
class FocusRestorer {
    var target: FocusRequester? = null
}

val LocalFocusRestorer = staticCompositionLocalOf { FocusRestorer() }
