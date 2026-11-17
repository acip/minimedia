package io.github.acip.minimedia.ui.player

import android.view.KeyEvent as NativeKeyEvent
import android.view.ViewGroup
import androidx.activity.compose.BackHandler
import androidx.annotation.OptIn
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusProperties
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.onKeyEvent
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.ui.AspectRatioFrameLayout
import androidx.media3.ui.CaptionStyleCompat
import androidx.media3.ui.PlayerView
import androidx.media3.ui.SubtitleView
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import io.github.acip.minimedia.data.Marker
import io.github.acip.minimedia.data.PlayerSettings
import io.github.acip.minimedia.data.Session
import io.github.acip.minimedia.graph
import io.github.acip.minimedia.ui.Accent
import io.github.acip.minimedia.ui.ActionButton
import io.github.acip.minimedia.ui.AppIcons
import io.github.acip.minimedia.ui.Muted
import io.github.acip.minimedia.ui.StartMode
import io.github.acip.minimedia.ui.verticalFade
import io.github.acip.minimedia.util.endsAt
import io.github.acip.minimedia.util.episodeCode
import io.github.acip.minimedia.util.formatClock
import kotlinx.coroutines.delay
import kotlin.math.abs

private const val JUMP_MS = 10_000L
private const val MEDIA_JUMP_MS = 30_000L
private const val HIDE_AFTER_MS = 5_000L
private const val SCRUB_COMMIT_MS = 2_000L
private const val SKIP_SHOWN_MS = 10_000L
private const val NEXT_COUNTDOWN_S = 10
private const val STILL_WATCHING_AFTER = 3

private enum class Panel { None, Tracks, Speed, Episodes }

@OptIn(UnstableApi::class)
@Composable
fun PlayerScreen(
    itemId: String,
    startMode: String,
    session: Session,
    onExit: () -> Unit,
    onPlayNext: (String, String) -> Unit,
) {
    val vm: PlayerViewModel = viewModel(key = "player-$itemId")
    val graph = LocalContext.current.graph
    val settings by graph.playerSettings.settings.collectAsState()
    // Before the PlayerView below is created: start() picks the player (one
    // prepared ahead by the Preloader, or a new one).
    remember(itemId) { vm.start(itemId, startMode) }
    LifecycleEventEffect(Lifecycle.Event.ON_STOP) { vm.onStop() }

    val view = LocalView.current
    DisposableEffect(Unit) {
        view.keepScreenOn = true
        onDispose { view.keepScreenOn = false }
    }

    var controlsShown by remember { mutableStateOf(true) }
    var scrubbingNow by remember { mutableStateOf(false) }
    Box(Modifier.fillMaxSize().background(Color.Black)) {
        AndroidView(
            factory = { ctx ->
                PlayerView(ctx).apply {
                    useController = false
                    player = vm.player
                    resizeMode = AspectRatioFrameLayout.RESIZE_MODE_FIT
                    setShowBuffering(PlayerView.SHOW_BUFFERING_NEVER)
                    setKeepContentOnPlayerReset(true)
                    isFocusable = false
                    descendantFocusability = ViewGroup.FOCUS_BLOCK_DESCENDANTS
                }
            },
            update = {
                applySubtitleStyle(it.subtitleView, settings, controlsShown)
                // Subtitles would cover the scrub thumbnail.
                it.subtitleView?.visibility = if (scrubbingNow) android.view.View.INVISIBLE else android.view.View.VISIBLE
            },
            onRelease = { it.player = null },
            modifier = Modifier.fillMaxSize(),
        )
        when (val s = vm.state) {
            PlayerUiState.Loading -> {
                Spinner(Modifier.align(Alignment.Center))
                BackHandler(onBack = onExit)
            }
            is PlayerUiState.AskResume -> ResumeDialog(s.resumeMs, onResume = { vm.begin(s.resumeMs) }, onStartOver = { vm.begin(0) }, onBack = onExit)
            is PlayerUiState.Error -> ErrorPanel(s.message, onExit)
            PlayerUiState.Playing -> Controls(vm, session, settings, onExit, onPlayNext, controlsVisible = { controlsShown = it }, scrubbing = { scrubbingNow = it })
        }
    }
}

/** Subtitle appearance from Settings (PLAN.md 8.4 "Subtitles"). */
@OptIn(UnstableApi::class)
private fun applySubtitleStyle(view: SubtitleView?, s: PlayerSettings, controlsShown: Boolean) {
    view ?: return
    val background = if (s.subtitleBackground == "box") 0xB0000000.toInt() else android.graphics.Color.TRANSPARENT
    val edge = when (s.subtitleBackground) {
        "shadow" -> CaptionStyleCompat.EDGE_TYPE_DROP_SHADOW
        "none" -> CaptionStyleCompat.EDGE_TYPE_NONE
        else -> CaptionStyleCompat.EDGE_TYPE_NONE
    }
    view.setStyle(CaptionStyleCompat(android.graphics.Color.WHITE, background, android.graphics.Color.TRANSPARENT,
        edge, android.graphics.Color.BLACK, null))
    view.setFractionalTextSize(SubtitleView.DEFAULT_TEXT_SIZE_FRACTION * s.sizeScale)
    val base = if (s.subtitlePosition == "raised") 0.16f else SubtitleView.DEFAULT_BOTTOM_PADDING_FRACTION
    // Subtitles move up while the controls cover the bottom of the picture.
    view.setBottomPaddingFraction(if (controlsShown) maxOf(base, 0.32f) else base)
}

@OptIn(UnstableApi::class)
@Composable
private fun Controls(
    vm: PlayerViewModel,
    session: Session,
    settings: PlayerSettings,
    onExit: () -> Unit,
    onPlayNext: (String, String) -> Unit,
    controlsVisible: (Boolean) -> Unit,
    scrubbing: (Boolean) -> Unit,
) {
    val player = vm.player
    val ctx = LocalContext.current
    val graph = ctx.graph

    // Player state, polled (cheap) so position, "Ends at" and buffering stay live.
    var position by remember { mutableLongStateOf(0L) }
    var duration by remember { mutableLongStateOf(vm.info?.durationMs ?: 0L) }
    var paused by remember { mutableStateOf(false) }
    var bufferingSince by remember { mutableLongStateOf(0L) }
    var now by remember { mutableLongStateOf(System.currentTimeMillis()) }
    LaunchedEffect(player) {
        while (true) {
            position = player.currentPosition
            player.duration.takeIf { it > 0 }?.let { duration = it }
            paused = !player.playWhenReady
            val buffering = player.playbackState == Player.STATE_BUFFERING
            bufferingSince = if (!buffering) 0L else if (bufferingSince == 0L) System.currentTimeMillis() else bufferingSince
            now = System.currentTimeMillis()
            delay(200)
        }
    }

    var visible by remember { mutableStateOf(true) }
    var panel by remember { mutableStateOf(Panel.None) }
    var lastInput by remember { mutableLongStateOf(System.currentTimeMillis()) }
    var scrubTarget by remember { mutableStateOf<Long?>(null) }
    var lastScrub by remember { mutableLongStateOf(0L) }
    var badge by remember { mutableStateOf<Pair<Long, Long>?>(null) } // accumulated ms, last press time
    var badgeShownAt by remember { mutableLongStateOf(0L) }
    var seekFocused by remember { mutableStateOf(false) }
    val rootFocus = remember { FocusRequester() }
    val seekFocus = remember { FocusRequester() }
    val playFocus = remember { FocusRequester() }
    val toSeek = Modifier.focusProperties { up = seekFocus }
    val next = vm.detail?.nextEpisode
    val isEpisode = vm.detail?.item?.isEpisode == true

    fun show() { visible = true; lastInput = System.currentTimeMillis() }
    fun commitScrub() {
        scrubTarget?.let { vm.seekTo(it, fast = true) }
        scrubTarget = null
    }
    fun jump(delta: Long) {
        val t = System.currentTimeMillis()
        val acc = badge?.takeIf { t - it.second < 1000 && (it.first > 0) == (delta > 0) }?.first ?: 0L
        badge = (acc + delta) to t
        badgeShownAt = t
        vm.seekBy(delta)
    }
    fun playNext(byAutoplay: Boolean) {
        val n = next ?: return
        graph.autoplayStreak = if (byAutoplay) graph.autoplayStreak + 1 else 0
        onPlayNext(n.id, StartMode.BEGINNING)
    }

    // ---- skip intro / recap (PLAN.md 8.4) ----
    val marker: Marker? = vm.info?.markers?.firstOrNull { (it.kind == "intro" || it.kind == "recap") && position in it.startMs until it.endMs }
    var skipFor by remember { mutableStateOf<Marker?>(null) }
    var skipSince by remember { mutableLongStateOf(0L) }
    LaunchedEffect(vm.seeks) { skipSince = System.currentTimeMillis() }
    if (marker != skipFor) {
        skipFor = marker
        skipSince = now
    }
    val skipVisible = marker != null && now - skipSince < SKIP_SHOWN_MS && scrubTarget == null
    fun skip() { marker?.let { vm.seekTo(it.endMs) } }

    // ---- next episode card / still watching (PLAN.md 8.4) ----
    var cardDismissed by remember { mutableStateOf(false) }
    val nextUpAt = vm.info?.nextUpAtMs ?: 0L
    val atCredits = next != null && nextUpAt > 0 && position >= nextUpAt && scrubTarget == null
    val stillWatching = atCredits && settings.stillWatching && graph.autoplayStreak >= STILL_WATCHING_AFTER
    val cardVisible = atCredits && !cardDismissed && !stillWatching
    var secondsLeft by remember { mutableIntStateOf(NEXT_COUNTDOWN_S) }
    LaunchedEffect(cardVisible, settings.autoplayNext) {
        if (!cardVisible || !settings.autoplayNext) return@LaunchedEffect
        for (s in NEXT_COUNTDOWN_S downTo 1) {
            secondsLeft = s
            delay(1000)
        }
        playNext(byAutoplay = true)
    }
    if (atCredits && position < nextUpAt) cardDismissed = false
    // When the file ends: autoplay the next episode unless dismissed, else leave.
    LaunchedEffect(vm.ended) {
        if (!vm.ended) return@LaunchedEffect
        if (next != null && settings.autoplayNext && !cardDismissed && !stillWatching) playNext(byAutoplay = true) else if (!stillWatching) onExit()
    }
    if (stillWatching) {
        LaunchedEffect(Unit) { player.pause() }
    }

    // Auto-hide after 5 s without input while playing (never while paused, scrubbing or in a panel).
    LaunchedEffect(visible, lastInput, paused, scrubTarget, panel) {
        if (visible && !paused && scrubTarget == null && panel == Panel.None) {
            delay(HIDE_AFTER_MS)
            visible = false
        }
    }
    // Scrub commits by itself 2 s after the last press.
    LaunchedEffect(lastScrub) {
        if (scrubTarget != null) {
            delay(SCRUB_COMMIT_MS)
            commitScrub()
        }
    }
    LaunchedEffect(badgeShownAt) {
        if (badge != null) {
            delay(1200)
            badge = null
        }
    }
    LaunchedEffect(visible, panel) {
        if (panel != Panel.None) return@LaunchedEffect
        runCatching { if (visible) seekFocus.requestFocus() else rootFocus.requestFocus() }
    }
    // Fallback if focus is somewhere the key handler below doesn't see.
    BackHandler {
        when {
            panel != Panel.None -> panel = Panel.None
            scrubTarget != null -> scrubTarget = null
            visible -> visible = false
            else -> onExit()
        }
    }
    LaunchedEffect(visible) { controlsVisible(visible) }
    LaunchedEffect(scrubTarget != null) { scrubbing(scrubTarget != null) }

    Box(
        Modifier
            .fillMaxSize()
            .focusRequester(rootFocus)
            .focusable()
            .onPreviewKeyEvent { e ->
                val code = e.nativeKeyEvent.keyCode
                // Back is handled here, not by a BackHandler: Compose first uses Back
                // to move focus out of the button row, which made presses seem lost.
                if (code == NativeKeyEvent.KEYCODE_BACK) {
                    if (e.type == KeyEventType.KeyUp) {
                        when {
                            panel != Panel.None -> panel = Panel.None
                            stillWatching -> onExit()
                            scrubTarget != null -> scrubTarget = null
                            visible -> visible = false
                            cardVisible -> cardDismissed = true // keep watching the credits
                            else -> onExit()
                        }
                    }
                    return@onPreviewKeyEvent true
                }
                if (e.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                lastInput = System.currentTimeMillis()
                graph.autoplayStreak = 0 // any remote input means someone is watching
                // Media keys work in every state.
                when (code) {
                    NativeKeyEvent.KEYCODE_MEDIA_PLAY_PAUSE, NativeKeyEvent.KEYCODE_MEDIA_PLAY, NativeKeyEvent.KEYCODE_MEDIA_PAUSE -> {
                        vm.togglePause(); show(); return@onPreviewKeyEvent true
                    }
                    NativeKeyEvent.KEYCODE_MEDIA_FAST_FORWARD -> { jump(MEDIA_JUMP_MS); return@onPreviewKeyEvent true }
                    NativeKeyEvent.KEYCODE_MEDIA_REWIND -> { jump(-MEDIA_JUMP_MS); return@onPreviewKeyEvent true }
                }
                if (visible || panel != Panel.None || stillWatching) return@onPreviewKeyEvent false
                // Controls hidden (PLAN.md 8.4).
                when (code) {
                    NativeKeyEvent.KEYCODE_DPAD_CENTER, NativeKeyEvent.KEYCODE_ENTER -> {
                        when {
                            skipVisible -> skip()
                            cardVisible -> playNext(byAutoplay = false)
                            else -> { vm.togglePause(); show() }
                        }
                        true
                    }
                    NativeKeyEvent.KEYCODE_DPAD_LEFT -> { jump(-JUMP_MS); true }
                    NativeKeyEvent.KEYCODE_DPAD_RIGHT -> { jump(JUMP_MS); true }
                    NativeKeyEvent.KEYCODE_DPAD_UP, NativeKeyEvent.KEYCODE_DPAD_DOWN -> { show(); true }
                    else -> false
                }
            },
    ) {
        // Buffering spinner, only after 500 ms so quick seeks don't flicker.
        if (bufferingSince > 0 && now - bufferingSince > 500) Spinner(Modifier.align(Alignment.Center))

        badge?.let { (ms, _) ->
            Text(
                (if (ms > 0) "+" else "−") + "${abs(ms) / 1000}",
                style = MaterialTheme.typography.displaySmall,
                fontWeight = FontWeight.Bold,
                modifier = Modifier
                    .align(if (ms > 0) Alignment.CenterEnd else Alignment.CenterStart)
                    .padding(horizontal = 96.dp)
                    .background(Color(0x99000000), RoundedCornerShape(12.dp))
                    .padding(horizontal = 24.dp, vertical = 12.dp),
            )
        }

        vm.warning?.let { w ->
            LaunchedEffect(w) { delay(8000); vm.clearWarning() }
            Text(
                w, style = MaterialTheme.typography.bodyMedium,
                modifier = Modifier.align(Alignment.TopEnd).padding(32.dp).width(420.dp)
                    .background(Color(0xCC201010), RoundedCornerShape(8.dp)).padding(14.dp),
            )
        }

        AnimatedVisibility(visible, enter = fadeIn(), exit = fadeOut(), modifier = Modifier.fillMaxSize()) {
            Box(Modifier.fillMaxSize()) {
                // Dimmed scrim while paused; otherwise fades behind the title and the
                // controls only (stretched bitmaps: full-screen gradients are slow on TV GPUs).
                if (paused) {
                    Box(Modifier.fillMaxSize().background(Color(0x99000000)))
                } else {
                    Box(Modifier.align(Alignment.TopCenter).fillMaxWidth().fillMaxHeight(0.25f).verticalFade(0f to 0.67f, 1f to 0f))
                    Box(Modifier.align(Alignment.BottomCenter).fillMaxWidth().fillMaxHeight(0.4f).verticalFade(0f to 0f, 1f to 0.87f))
                }
                TitleBlock(vm, Modifier.align(Alignment.TopStart).padding(48.dp))

                Column(Modifier.align(Alignment.BottomCenter).fillMaxWidth().padding(horizontal = 48.dp, vertical = 32.dp)) {
                    val shown = scrubTarget ?: position
                    val fraction = if (duration > 0) (shown.toFloat() / duration).coerceIn(0f, 1f) else 0f
                    // Scrub preview above the head: thumbnail + target time (PLAN.md 8.4).
                    if (scrubTarget != null) {
                        BoxWithConstraints(Modifier.fillMaxWidth().padding(bottom = 10.dp)) {
                            val w = 240.dp
                            val x = (maxWidth * fraction - w / 2).coerceIn(0.dp, maxWidth - w)
                            ScrubPreview(vm.trickplay, shown, w, Modifier.offset(x = x).width(w))
                        }
                    }
                    SeekBar(
                        fraction = fraction,
                        scrubbing = scrubTarget != null,
                        focused = seekFocused,
                        modifier = Modifier
                            .focusRequester(seekFocus)
                            .focusProperties { down = playFocus }
                            .onFocusChanged { seekFocused = it.isFocused }
                            .focusable()
                            .onKeyEvent { e ->
                                if (e.type != KeyEventType.KeyDown) return@onKeyEvent false
                                val ne = e.nativeKeyEvent
                                when (ne.keyCode) {
                                    NativeKeyEvent.KEYCODE_DPAD_LEFT, NativeKeyEvent.KEYCODE_DPAD_RIGHT -> {
                                        // Holding accelerates: 10 s → 30 s after 1 s → 60 s after 3 s
                                        // (→ 2 min after 6 s on files over 2 h).
                                        val held = ne.eventTime - ne.downTime
                                        val step = when {
                                            held >= 6000 && duration > 2 * 3_600_000 -> 120_000L
                                            held >= 3000 -> 60_000L
                                            held >= 1000 -> 30_000L
                                            else -> JUMP_MS
                                        }
                                        val delta = if (ne.keyCode == NativeKeyEvent.KEYCODE_DPAD_LEFT) -step else step
                                        scrubTarget = ((scrubTarget ?: player.currentPosition) + delta).coerceIn(0, maxOf(duration - 1000, 0))
                                        lastScrub = System.currentTimeMillis()
                                        true
                                    }
                                    NativeKeyEvent.KEYCODE_DPAD_CENTER, NativeKeyEvent.KEYCODE_ENTER -> {
                                        if (scrubTarget != null) commitScrub() else vm.togglePause()
                                        true
                                    }
                                    else -> false
                                }
                            },
                    )
                    Row(Modifier.fillMaxWidth().padding(top = 6.dp)) {
                        Text(formatClock(shown), style = MaterialTheme.typography.bodyLarge)
                        Spacer(Modifier.weight(1f))
                        Text("Ends at ${endsAt(ctx, duration - shown, vm.speed, now)}", style = MaterialTheme.typography.bodyLarge, color = Muted)
                        Spacer(Modifier.weight(1f))
                        Text("-" + formatClock(duration - shown), style = MaterialTheme.typography.bodyLarge)
                    }
                    Row(Modifier.padding(top = 14.dp), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                        ActionButton(if (paused) "Play" else "Pause", onClick = { vm.togglePause(); show() }, modifier = toSeek.focusRequester(playFocus),
                            icon = if (paused) AppIcons.Play else AppIcons.Pause)
                        ActionButton("Audio & Subtitles", onClick = { panel = Panel.Tracks }, modifier = toSeek)
                        ActionButton("Speed ${speedLabel(vm.speed)}", onClick = { panel = Panel.Speed }, modifier = toSeek)
                        if (isEpisode) ActionButton("Episodes", onClick = { panel = Panel.Episodes }, modifier = toSeek)
                        if (next != null) {
                            ActionButton("Next episode", onClick = { playNext(byAutoplay = false) }, icon = AppIcons.Next, modifier = toSeek)
                        }
                    }
                }
            }
        }

        // Skip Intro / Recap, bottom-right, also while the controls are hidden.
        if (skipVisible && panel == Panel.None) {
            ActionButton(
                if (marker.kind == "recap") "Skip Recap" else "Skip Intro",
                onClick = { skip() },
                prominent = true,
                modifier = Modifier.align(Alignment.BottomEnd).padding(end = 48.dp, bottom = if (visible) 150.dp else 48.dp),
            )
        }
        if (cardVisible && panel == Panel.None) {
            NextEpisodeCard(
                next, session,
                secondsLeft = if (settings.autoplayNext) secondsLeft else null,
                onPlayNow = { playNext(byAutoplay = false) },
                modifier = Modifier.align(Alignment.BottomEnd).padding(end = 48.dp, bottom = if (visible) 150.dp else 48.dp),
            )
        }
        when (panel) {
            Panel.Tracks -> TracksPanel(vm)
            Panel.Speed -> SpeedPanel(vm, onDone = { panel = Panel.None })
            Panel.Episodes -> EpisodesPanel(vm, session, onPlay = { onPlayNext(it.id, StartMode.ASK) })
            Panel.None -> Unit
        }
        if (stillWatching) {
            StillWatching(
                onContinue = { graph.autoplayStreak = 0; playNext(byAutoplay = false) },
                onStop = onExit,
            )
        }
    }
    LaunchedEffect(Unit) { runCatching { seekFocus.requestFocus() } }
}

@Composable
private fun TitleBlock(vm: PlayerViewModel, modifier: Modifier) {
    val m = vm.detail?.item ?: return
    Column(modifier) {
        if (m.isEpisode) {
            Text(m.showTitle.orEmpty(), style = MaterialTheme.typography.headlineSmall, fontWeight = FontWeight.Bold)
            Text(listOf(episodeCode(m), m.title).filter { it.isNotEmpty() }.joinToString(" · "),
                style = MaterialTheme.typography.titleMedium, color = Muted)
        } else {
            Text(m.title, style = MaterialTheme.typography.headlineSmall, fontWeight = FontWeight.Bold,
                maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
    }
}

@Composable
private fun SeekBar(fraction: Float, scrubbing: Boolean, focused: Boolean, modifier: Modifier) {
    BoxWithConstraints(modifier.fillMaxWidth().height(28.dp)) {
        val barHeight = if (focused) 6.dp else 4.dp
        Box(
            Modifier.align(Alignment.CenterStart).fillMaxWidth().height(barHeight).clip(RoundedCornerShape(3.dp))
                .background(Color(0x55FFFFFF)),
        ) {
            Box(Modifier.fillMaxWidth(fraction).height(barHeight).background(Accent))
        }
        val head = if (focused) 18.dp else 12.dp
        Box(
            Modifier.align(Alignment.CenterStart).offset(x = maxWidth * fraction - head / 2).size(head).clip(CircleShape)
                .background(if (scrubbing || focused) Color.White else Accent),
        )
    }
}

@Composable
private fun ResumeDialog(resumeMs: Long, onResume: () -> Unit, onStartOver: () -> Unit, onBack: () -> Unit) {
    val first = remember { FocusRequester() }
    BackHandler(onBack = onBack)
    Box(Modifier.fillMaxSize().background(Color(0xCC000000)), contentAlignment = Alignment.Center) {
        Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(14.dp)) {
            ActionButton("Resume from ${formatClock(resumeMs)}", onClick = onResume, icon = AppIcons.Play,
                modifier = Modifier.focusRequester(first).width(320.dp))
            ActionButton("Start from beginning", onClick = onStartOver, icon = AppIcons.Restart, modifier = Modifier.width(320.dp))
        }
    }
    LaunchedEffect(Unit) { runCatching { first.requestFocus() } }
}

@Composable
private fun ErrorPanel(message: String, onBack: () -> Unit) {
    val focus = remember { FocusRequester() }
    BackHandler(onBack = onBack)
    Box(Modifier.fillMaxSize().background(Color(0xEE000000)), contentAlignment = Alignment.Center) {
        Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(20.dp)) {
            Text(message, style = MaterialTheme.typography.titleLarge, modifier = Modifier.width(640.dp))
            ActionButton("Back", onClick = onBack, modifier = Modifier.focusRequester(focus))
        }
    }
    LaunchedEffect(Unit) { runCatching { focus.requestFocus() } }
}

@Composable
private fun Spinner(modifier: Modifier = Modifier) {
    val t = rememberInfiniteTransition(label = "spinner")
    val angle by t.animateFloat(0f, 360f, infiniteRepeatable(tween(900, easing = LinearEasing), RepeatMode.Restart), label = "angle")
    Canvas(modifier.size(56.dp)) {
        drawArc(Color.White, startAngle = angle, sweepAngle = 270f, useCenter = false,
            topLeft = Offset(4f, 4f), size = Size(size.width - 8f, size.height - 8f), style = Stroke(width = 6f, cap = StrokeCap.Round))
    }
}
