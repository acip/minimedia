package io.github.acip.minimedia.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.tv.material3.LocalContentColor
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.darkColorScheme

val Accent = Color(0xFFE50914)
val Muted = Color(0xFFB3B3B3)
val CardBg = Color(0xFF22252E)

@Composable
fun MiniMediaTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = darkColorScheme(
            primary = Color.White,
            onPrimary = Color.Black,
            secondary = Accent,
            background = Color.Black,
            surface = Color(0xFF141414),
            onSurface = Color.White,
            surfaceVariant = CardBg,
            onSurfaceVariant = Muted,
            border = Color.White,
        ),
    ) {
        // tv-material's Text is black outside a Surface; the app is white-on-dark everywhere.
        CompositionLocalProvider(LocalContentColor provides Color.White) {
            Box(Modifier.fillMaxSize().background(Color.Black)) { content() }
        }
    }
}
