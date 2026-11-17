package io.github.acip.minimedia.ui

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import kotlinx.coroutines.delay
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.tv.material3.Border
import androidx.tv.material3.Button
import androidx.tv.material3.ButtonDefaults
import androidx.tv.material3.Card
import androidx.tv.material3.CardDefaults
import androidx.tv.material3.Icon
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import coil3.compose.AsyncImage
import io.github.acip.minimedia.data.Media
import io.github.acip.minimedia.data.Session
import io.github.acip.minimedia.util.episodeCode
import io.github.acip.minimedia.util.formatRuntime
import io.github.acip.minimedia.util.progressFraction

private val cardShape = RoundedCornerShape(8.dp)

@Composable
private fun cardBorder() = CardDefaults.border(
    focusedBorder = Border(BorderStroke(3.dp, Color.White), shape = cardShape),
)

/** 2:3 poster card; titles without a poster get a text placeholder. */
@Composable
fun PosterCard(
    m: Media,
    session: Session,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    width: Dp = 150.dp,
    onFocus: () -> Unit = {},
) {
    Column(modifier.width(width)) {
        Card(
            onClick = onClick,
            modifier = Modifier
                .fillMaxWidth()
                .aspectRatio(2f / 3f)
                .onFocusChanged { if (it.isFocused) onFocus() },
            shape = CardDefaults.shape(cardShape),
            border = cardBorder(),
            scale = CardDefaults.scale(focusedScale = 1.06f),
            colors = CardDefaults.colors(containerColor = CardBg),
        ) {
            Box(Modifier.fillMaxSize()) {
                val poster = session.image(m.posterUrl, 342)
                if (poster != null) {
                    AsyncImage(poster, m.title, Modifier.fillMaxSize(), contentScale = ContentScale.Crop)
                } else {
                    TitlePlaceholder(m.title, m.year)
                }
                CardOverlays(m)
            }
        }
    }
}

/** 16:9 card for episodes and continue-watching rows, with a caption below. */
@Composable
fun LandscapeCard(
    m: Media,
    session: Session,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    width: Dp = 260.dp,
    onFocus: () -> Unit = {},
) {
    Column(modifier.width(width)) {
        Card(
            onClick = onClick,
            modifier = Modifier
                .fillMaxWidth()
                .aspectRatio(16f / 9f)
                .onFocusChanged { if (it.isFocused) onFocus() },
            shape = CardDefaults.shape(cardShape),
            border = cardBorder(),
            scale = CardDefaults.scale(focusedScale = 1.05f),
            colors = CardDefaults.colors(containerColor = CardBg),
        ) {
            Box(Modifier.fillMaxSize()) {
                val img = session.image(m.stillUrl ?: m.backdropUrl, 780)
                if (img != null) {
                    AsyncImage(img, m.title, Modifier.fillMaxSize(), contentScale = ContentScale.Crop)
                } else {
                    TitlePlaceholder(m.showTitle ?: m.title, 0)
                }
                CardOverlays(m)
            }
        }
        Spacer(Modifier.height(8.dp))
        val (line1, line2) = captionLines(m)
        Text(line1, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
        if (line2.isNotEmpty()) {
            Text(line2, style = MaterialTheme.typography.bodySmall, color = Muted, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
    }
}

fun captionLines(m: Media): Pair<String, String> = when {
    m.isEpisode -> (m.showTitle ?: "") to listOf(episodeCode(m), m.title).filter { it.isNotEmpty() }.joinToString(" · ")
    else -> m.title to listOfNotNull(m.year.takeIf { it > 0 }?.toString(), formatRuntime(m.runtimeMs).ifEmpty { null }).joinToString(" · ")
}

@Composable
private fun BoxScope.CardOverlays(m: Media) {
    progressFraction(m)?.let { f ->
        ProgressBar(f, Modifier.align(Alignment.BottomCenter).fillMaxWidth().padding(8.dp))
    }
    if (m.watched) {
        Box(
            Modifier.align(Alignment.TopEnd).padding(6.dp).size(24.dp).clip(CircleShape).background(Color(0xCC000000)),
            contentAlignment = Alignment.Center,
        ) { Icon(AppIcons.Check, "Watched", Modifier.size(16.dp), tint = Color.White) }
    }
    if (m.isShow && m.unwatchedCount > 0) {
        Box(
            Modifier.align(Alignment.TopEnd).padding(6.dp).clip(RoundedCornerShape(12.dp)).background(Accent)
                .padding(horizontal = 8.dp, vertical = 2.dp),
        ) { Text("${m.unwatchedCount}", style = MaterialTheme.typography.labelMedium, color = Color.White) }
    }
}

@Composable
private fun TitlePlaceholder(title: String, year: Int) {
    Box(
        Modifier.fillMaxSize().background(Color(0xFF22252E)).padding(12.dp),
        contentAlignment = Alignment.Center,
    ) {
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            Text(title, style = MaterialTheme.typography.titleMedium, textAlign = TextAlign.Center, maxLines = 4, overflow = TextOverflow.Ellipsis)
            if (year > 0) Text("$year", style = MaterialTheme.typography.bodySmall, color = Muted)
        }
    }
}

@Composable
fun ProgressBar(fraction: Float, modifier: Modifier = Modifier, height: Dp = 4.dp) {
    Box(modifier.height(height).clip(RoundedCornerShape(2.dp)).background(Color(0x66FFFFFF))) {
        Box(Modifier.fillMaxHeight().fillMaxWidth(fraction.coerceIn(0f, 1f)).background(Accent))
    }
}

/** A titled horizontal row of cards. */
@Composable
fun MediaRow(
    title: String,
    items: List<Media>,
    session: Session,
    landscape: Boolean,
    onClick: (Media) -> Unit,
    onFocus: (Media) -> Unit = {},
    modifier: Modifier = Modifier,
    itemModifier: (Media) -> Modifier = { Modifier },
) {
    if (items.isEmpty()) return
    Column(modifier.fillMaxWidth()) {
        Text(
            title,
            style = MaterialTheme.typography.titleLarge,
            fontWeight = FontWeight.SemiBold,
            modifier = Modifier.padding(start = 48.dp, bottom = 12.dp),
        )
        LazyRow(
            contentPadding = PaddingValues(horizontal = 48.dp, vertical = 8.dp),
            horizontalArrangement = Arrangement.spacedBy(20.dp),
        ) {
            items(items, key = { it.id }) { m ->
                if (landscape) {
                    LandscapeCard(m, session, onClick = { onClick(m) }, onFocus = { onFocus(m) }, modifier = itemModifier(m))
                } else {
                    PosterCard(m, session, onClick = { onClick(m) }, onFocus = { onFocus(m) }, modifier = itemModifier(m))
                }
            }
        }
    }
}

/** A remote-friendly button with an optional icon. */
@Composable
fun ActionButton(
    text: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    icon: ImageVector? = null,
    enabled: Boolean = true,
    prominent: Boolean = false, // solid white, for actions OK triggers even without focus
) {
    Button(
        onClick = onClick,
        modifier = modifier,
        enabled = enabled,
        contentPadding = PaddingValues(horizontal = 20.dp, vertical = 10.dp),
        colors = ButtonDefaults.colors(
            containerColor = if (prominent) Color.White else Color(0x33FFFFFF),
            contentColor = if (prominent) Color.Black else Color.White,
            focusedContainerColor = Color.White,
            focusedContentColor = Color.Black,
        ),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            if (icon != null) {
                Icon(icon, null, Modifier.size(20.dp))
                Spacer(Modifier.width(8.dp))
            }
            Text(text, style = MaterialTheme.typography.titleSmall)
        }
    }
}

/**
 * Full-screen backdrop, darkened on the left and bottom so text stays
 * readable. The darkening is painted into the image when it loads
 * (BackdropScrim), not drawn every frame.
 */
@Composable
fun Backdrop(url: String?, modifier: Modifier = Modifier) {
    if (url == null) return
    val ctx = LocalContext.current
    val request = remember(url) { backdropRequest(ctx, url) }
    AsyncImage(
        request, null,
        modifier.fillMaxSize(),
        contentScale = ContentScale.Crop,
        alignment = Alignment.TopEnd,
    )
}

@Composable
fun CenteredMessage(text: String, modifier: Modifier = Modifier, action: (@Composable () -> Unit)? = null) {
    Box(modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(20.dp)) {
            Text(text, style = MaterialTheme.typography.titleMedium, color = Muted, textAlign = TextAlign.Center)
            action?.invoke()
        }
    }
}

/** An error at once; "Loading…" only when loading is slow (a flash of it on every quick load looks sluggish). */
@Composable
fun LoadingOrError(error: String?) {
    if (error != null) {
        CenteredMessage(error)
        return
    }
    var show by remember { mutableStateOf(false) }
    LaunchedEffect(Unit) {
        delay(600)
        show = true
    }
    if (show) CenteredMessage("Loading…")
}

@Composable
fun rememberContext() = LocalContext.current
