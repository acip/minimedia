package io.github.acip.minimedia.data

import android.content.Context
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn

/** Player settings (PLAN.md 8.3 "Settings"). */
data class PlayerSettings(
    val subtitleSize: String = "M",            // S | M | L | XL
    val subtitleBackground: String = "shadow", // none | shadow | box
    val subtitlePosition: String = "bottom",   // bottom | raised
    val autoplayNext: Boolean = true,
    val stillWatching: Boolean = true,
) {
    val sizeScale: Float
        get() = when (subtitleSize) {
            "S" -> 0.8f
            "L" -> 1.25f
            "XL" -> 1.5f
            else -> 1f
        }

    companion object {
        val sizes = listOf("S", "M", "L", "XL")
        val backgrounds = listOf("none", "shadow", "box")
        val positions = listOf("bottom", "raised")
    }
}

private val Context.playerStore by preferencesDataStore("player")

class PlayerSettingsStore(context: Context, scope: CoroutineScope) {
    private val store = context.applicationContext.playerStore
    private val size = stringPreferencesKey("subtitle_size")
    private val background = stringPreferencesKey("subtitle_background")
    private val position = stringPreferencesKey("subtitle_position")
    private val autoplay = booleanPreferencesKey("autoplay_next")
    private val still = booleanPreferencesKey("still_watching")

    val settings: StateFlow<PlayerSettings> = store.data.map { p ->
        val d = PlayerSettings()
        PlayerSettings(
            subtitleSize = p[size] ?: d.subtitleSize,
            subtitleBackground = p[background] ?: d.subtitleBackground,
            subtitlePosition = p[position] ?: d.subtitlePosition,
            autoplayNext = p[autoplay] ?: d.autoplayNext,
            stillWatching = p[still] ?: d.stillWatching,
        )
    }.stateIn(scope, SharingStarted.Eagerly, PlayerSettings())

    suspend fun update(transform: (PlayerSettings) -> PlayerSettings) {
        store.edit { p ->
            val n = transform(settings.value)
            p[size] = n.subtitleSize
            p[background] = n.subtitleBackground
            p[position] = n.subtitlePosition
            p[autoplay] = n.autoplayNext
            p[still] = n.stillWatching
        }
    }
}
