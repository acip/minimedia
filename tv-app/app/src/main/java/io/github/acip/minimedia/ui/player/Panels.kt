package io.github.acip.minimedia.ui.player

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.tv.material3.ClickableSurfaceDefaults
import androidx.tv.material3.Icon
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Surface
import androidx.tv.material3.Text
import coil3.compose.AsyncImage
import io.github.acip.minimedia.data.Media
import io.github.acip.minimedia.data.Session
import io.github.acip.minimedia.ui.ActionButton
import io.github.acip.minimedia.ui.AppIcons
import io.github.acip.minimedia.ui.Muted
import io.github.acip.minimedia.util.episodeCode
import kotlinx.coroutines.delay

/** One row in a side panel: a check mark when selected. */
@Composable
private fun PanelItem(
    text: String,
    selected: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    detail: String? = null,
) {
    Surface(
        onClick = onClick,
        enabled = enabled,
        modifier = modifier.fillMaxWidth(),
        shape = ClickableSurfaceDefaults.shape(RoundedCornerShape(8.dp)),
        colors = ClickableSurfaceDefaults.colors(
            containerColor = Color.Transparent,
            contentColor = if (enabled) Color.White else Color(0x77FFFFFF),
            focusedContainerColor = Color.White,
            focusedContentColor = Color.Black,
            disabledContainerColor = Color.Transparent,
            disabledContentColor = Color(0x66FFFFFF),
        ),
        scale = ClickableSurfaceDefaults.scale(focusedScale = 1f),
    ) {
        Row(Modifier.padding(horizontal = 14.dp, vertical = 9.dp), verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.size(22.dp)) { if (selected) Icon(AppIcons.Check, null, Modifier.size(20.dp)) }
            Spacer(Modifier.width(10.dp))
            Column {
                Text(text, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                if (detail != null) Text(detail, style = MaterialTheme.typography.bodySmall, maxLines = 1)
            }
        }
    }
}

@Composable
private fun SidePanel(width: androidx.compose.ui.unit.Dp, content: @Composable () -> Unit) {
    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.CenterEnd) {
        Box(
            Modifier.fillMaxHeight().width(width).background(Color(0xEE141414)).padding(28.dp),
        ) { content() }
    }
}

/** Audio & Subtitles panel (PLAN.md 8.4): two columns, current choices checked. */
@Composable
fun TracksPanel(vm: PlayerViewModel) {
    val info = vm.info ?: return
    val first = remember { FocusRequester() }
    SidePanel(640.dp) {
        Row(horizontalArrangement = Arrangement.spacedBy(24.dp)) {
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text("Audio", style = MaterialTheme.typography.titleLarge, modifier = Modifier.padding(bottom = 8.dp))
                LazyColumn(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    itemsIndexed(info.audioTracks) { _, a ->
                        val selected = a.index == vm.audioIndex
                        PanelItem(
                            a.label, selected,
                            onClick = { vm.selectAudio(a.index) },
                            detail = if (!a.playable) "Converted for this device" else a.title.ifEmpty { null },
                            modifier = if (selected) Modifier.focusRequester(first) else Modifier,
                        )
                    }
                }
            }
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text("Subtitles", style = MaterialTheme.typography.titleLarge, modifier = Modifier.padding(bottom = 8.dp))
                LazyColumn(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    item {
                        PanelItem("Off", vm.subtitleId == null, onClick = { vm.selectSubtitle(null) },
                            modifier = if (info.audioTracks.isEmpty() && vm.subtitleId == null) Modifier.focusRequester(first) else Modifier)
                    }
                    itemsIndexed(info.subtitles) { _, s ->
                        PanelItem(s.label, s.id == vm.subtitleId, onClick = { vm.selectSubtitle(s.id) },
                            enabled = s.available,
                            detail = if (s.embedded) null else "External file")
                    }
                }
            }
        }
    }
    LaunchedEffect(Unit) { runCatching { first.requestFocus() } }
}

val SPEEDS = listOf(0.5f, 0.75f, 1f, 1.25f, 1.5f, 2f)

fun speedLabel(s: Float) = (if (s % 1f == 0f) "${s.toInt()}" else "$s") + "×"

@Composable
fun SpeedPanel(vm: PlayerViewModel, onDone: () -> Unit) {
    val focus = remember { FocusRequester() }
    SidePanel(300.dp) {
        Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text("Speed", style = MaterialTheme.typography.titleLarge, modifier = Modifier.padding(bottom = 8.dp))
            SPEEDS.forEach { s ->
                PanelItem(
                    if (s == 1f) "Normal" else speedLabel(s), s == vm.speed,
                    onClick = { vm.changeSpeed(s); onDone() },
                    modifier = if (s == vm.speed) Modifier.focusRequester(focus) else Modifier,
                )
            }
        }
    }
    LaunchedEffect(Unit) { runCatching { focus.requestFocus() } }
}

/** The episodes of the current season, to jump to another one. */
@Composable
fun EpisodesPanel(vm: PlayerViewModel, session: Session, onPlay: (Media) -> Unit) {
    LaunchedEffect(Unit) { vm.loadSeason() }
    val current = vm.detail?.item?.id
    val focus = remember { FocusRequester() }
    SidePanel(520.dp) {
        Column {
            Text("Episodes", style = MaterialTheme.typography.titleLarge, modifier = Modifier.padding(bottom = 12.dp))
            LazyColumn(verticalArrangement = Arrangement.spacedBy(8.dp), contentPadding = PaddingValues(bottom = 24.dp)) {
                itemsIndexed(vm.seasonEpisodes) { _, e ->
                    Surface(
                        onClick = { if (e.id != current) onPlay(e) },
                        modifier = Modifier.fillMaxWidth().then(if (e.id == current) Modifier.focusRequester(focus) else Modifier),
                        shape = ClickableSurfaceDefaults.shape(RoundedCornerShape(8.dp)),
                        colors = ClickableSurfaceDefaults.colors(
                            containerColor = Color.Transparent, contentColor = Color.White,
                            focusedContainerColor = Color(0x33FFFFFF), focusedContentColor = Color.White,
                        ),
                        scale = ClickableSurfaceDefaults.scale(focusedScale = 1f),
                    ) {
                        Row(Modifier.padding(8.dp), verticalAlignment = Alignment.CenterVertically) {
                            Box(Modifier.width(140.dp).aspectRatio(16f / 9f).clip(RoundedCornerShape(6.dp)).background(Color(0xFF222222))) {
                                session.image(e.stillUrl, 342)?.let { AsyncImage(it, null, Modifier.fillMaxSize(), contentScale = ContentScale.Crop) }
                            }
                            Spacer(Modifier.width(12.dp))
                            Column {
                                Text(episodeCode(e), style = MaterialTheme.typography.bodySmall, color = Muted)
                                Text(e.title, style = MaterialTheme.typography.titleSmall, maxLines = 2, overflow = TextOverflow.Ellipsis,
                                    fontWeight = if (e.id == current) FontWeight.Bold else FontWeight.Normal)
                                if (e.id == current) Text("Playing", style = MaterialTheme.typography.bodySmall, color = Muted)
                            }
                        }
                    }
                }
            }
        }
    }
    LaunchedEffect(vm.seasonEpisodes) {
        if (vm.seasonEpisodes.isNotEmpty()) {
            delay(50)
            runCatching { focus.requestFocus() }
        }
    }
}

/** "Next episode in 10…" card (PLAN.md 8.4). */
@Composable
fun NextEpisodeCard(next: Media, session: Session, secondsLeft: Int?, onPlayNow: () -> Unit, modifier: Modifier = Modifier) {
    Row(
        modifier.width(560.dp).background(Color(0xEE141414), RoundedCornerShape(12.dp)).padding(14.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(Modifier.width(200.dp).aspectRatio(16f / 9f).clip(RoundedCornerShape(8.dp)).background(Color(0xFF222222))) {
            session.image(next.stillUrl, 342)?.let { AsyncImage(it, null, Modifier.fillMaxSize(), contentScale = ContentScale.Crop) }
        }
        Spacer(Modifier.width(16.dp))
        Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text(
                if (secondsLeft != null) "Next episode in $secondsLeft…" else "Next episode",
                style = MaterialTheme.typography.bodyMedium, color = Muted,
            )
            Text(listOf(episodeCode(next), next.title).filter { it.isNotEmpty() }.joinToString(" · "),
                style = MaterialTheme.typography.titleMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
            ActionButton("Play now", onClick = onPlayNow, icon = AppIcons.Play, modifier = Modifier.padding(top = 6.dp))
        }
    }
}

/** Shown instead of autoplaying after 3 episodes in a row without input. */
@Composable
fun StillWatching(onContinue: () -> Unit, onStop: () -> Unit) {
    val focus = remember { FocusRequester() }
    Box(Modifier.fillMaxSize().background(Color(0xCC000000)), contentAlignment = Alignment.Center) {
        Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(16.dp)) {
            Text("Are you still watching?", style = MaterialTheme.typography.headlineMedium)
            Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                ActionButton("Continue watching", onClick = onContinue, icon = AppIcons.Play, modifier = Modifier.focusRequester(focus))
                ActionButton("Stop", onClick = onStop)
            }
        }
    }
    LaunchedEffect(Unit) { runCatching { focus.requestFocus() } }
}
