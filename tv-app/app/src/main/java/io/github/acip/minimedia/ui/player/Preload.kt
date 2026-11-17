package io.github.acip.minimedia.ui.player

import android.app.Application
import android.net.Uri
import androidx.annotation.OptIn
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.MimeTypes
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DefaultDataSource
import androidx.media3.datasource.DefaultHttpDataSource
import androidx.media3.exoplayer.DefaultLoadControl
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import io.github.acip.minimedia.data.AppGraph
import io.github.acip.minimedia.data.Capabilities
import io.github.acip.minimedia.data.PlaybackInfo
import io.github.acip.minimedia.data.Session
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Deferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.async
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

@OptIn(UnstableApi::class)
fun buildPlayer(app: Application): ExoPlayer = ExoPlayer.Builder(app)
    // The server may hold a playlist or segment request while it indexes
    // keyframes or converts audio (PLAN.md 4.3: up to 20 s), longer than
    // the default 8 s read timeout.
    .setMediaSourceFactory(
        DefaultMediaSourceFactory(
            DefaultDataSource.Factory(app, DefaultHttpDataSource.Factory().setConnectTimeoutMs(10_000).setReadTimeoutMs(45_000)),
        ),
    )
    .setAudioAttributes(
        AudioAttributes.Builder().setUsage(C.USAGE_MEDIA).setContentType(C.AUDIO_CONTENT_TYPE_MOVIE).build(),
        /* handleAudioFocus = */ true,
    )
    .setHandleAudioBecomingNoisy(true)
    // Start (and resume after a seek) once 0.5 s is buffered instead of 1 s:
    // on a home network the rest arrives well before it is needed.
    .setLoadControl(
        DefaultLoadControl.Builder()
            .setBufferDurationsMs(
                DefaultLoadControl.DEFAULT_MIN_BUFFER_MS,
                DefaultLoadControl.DEFAULT_MAX_BUFFER_MS,
                500,
                DefaultLoadControl.DEFAULT_BUFFER_FOR_PLAYBACK_AFTER_REBUFFER_MS,
            )
            .build(),
    )
    .build()
    .apply {
        // Switch the TV's refresh rate only when seamless; Google TV's
        // "Match content frame rate" setting decides the rest (PLAN.md 8.4).
        videoChangeFrameRateStrategy = C.VIDEO_CHANGE_FRAME_RATE_STRATEGY_ONLY_IF_SEAMLESS
    }

/** The media item for a playback decision: the stream plus side-loaded subtitles. */
fun mediaItemFor(session: Session, pi: PlaybackInfo, itemId: String): MediaItem {
    val sidecars = pi.subtitles.filter { it.url != null }.map { s ->
        MediaItem.SubtitleConfiguration.Builder(Uri.parse(session.url(s.url!!)))
            .setId(s.id)
            .setMimeType(s.mime.ifEmpty { null })
            .setLanguage(s.language)
            .setLabel(s.label)
            .setSelectionFlags(if (s.forced) C.SELECTION_FLAG_FORCED else 0)
            .build()
    }
    return MediaItem.Builder()
        .setUri(session.url(pi.url))
        .setMimeType(if (pi.mode == "hls") MimeTypes.APPLICATION_M3U8 else null)
        .setMediaId(itemId)
        .setSubtitleConfigurations(sidecars)
        .build()
}

/**
 * Starts the most likely next playback before Play is pressed, as streaming
 * apps do: on a details page, or after resting on a card that plays, the
 * playback decision is fetched and a paused player buffers the stream at the
 * resume point. Pressing Play then hands that player to the player screen,
 * which only has to attach the picture and start.
 *
 * At most one title is prepared at a time (TV hardware has few video
 * decoders), it is dropped after a few minutes unused, when another title is
 * prepared or played, and when the app goes to the background.
 */
class Preloader(private val app: Application, private val graph: AppGraph) {
    class Prepared(val itemId: String, val info: PlaybackInfo, val positionMs: Long, val player: ExoPlayer)

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private var prepared: Prepared? = null
    private var pendingId: String? = null
    private var pending: Deferred<PlaybackInfo?>? = null
    private var expiry: Job? = null

    /**
     * Prepares itemId at its resume point (or the start); returns the playback
     * decision, e.g. to tell whether the device can play it at all.
     * Call on the main thread.
     */
    fun preload(itemId: String): Deferred<PlaybackInfo?> {
        prepared?.let { if (it.itemId == itemId) return CompletableDeferred(it.info) }
        if (pendingId == itemId) pending?.let { return it }
        release()
        val job = scope.async {
            val session = graph.session ?: return@async null
            val caps = Capabilities.detect(app)
            val pi = runCatching { session.api.playback(itemId, caps) }.getOrNull() ?: return@async null
            if (pi.unsupportedReason != null) return@async pi
            val player = buildPlayer(app)
            player.setMediaItem(mediaItemFor(session, pi, itemId), pi.resumeMs)
            player.playWhenReady = false
            player.prepare()
            prepared = Prepared(itemId, pi, pi.resumeMs, player)
            expiry = scope.launch {
                delay(3 * 60_000)
                release()
            }
            pi
        }
        pendingId = itemId
        pending = job
        job.invokeOnCompletion { if (pending === job) { pending = null; pendingId = null } }
        return job
    }

    /**
     * The prepared player for itemId, now owned by the caller; null if another
     * title (or nothing) was prepared, which is then released to free the
     * decoder for the caller's own player.
     */
    fun take(itemId: String): Prepared? {
        val p = prepared?.takeIf { it.itemId == itemId }
        if (p == null) {
            release()
            return null
        }
        prepared = null
        expiry?.cancel()
        return p
    }

    fun release() {
        pending?.cancel()
        pending = null
        pendingId = null
        expiry?.cancel()
        prepared?.player?.release()
        prepared = null
    }
}
