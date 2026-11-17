package io.github.acip.minimedia.ui.player

import android.app.Application
import android.os.SystemClock
import androidx.annotation.OptIn
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.MimeTypes
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.TrackSelectionOverride
import androidx.media3.common.Tracks
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlaybackException
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.SeekParameters
import io.github.acip.minimedia.data.Capabilities
import io.github.acip.minimedia.data.ItemDetail
import io.github.acip.minimedia.data.PlaybackInfo
import io.github.acip.minimedia.data.ProgressReport
import io.github.acip.minimedia.graph
import io.github.acip.minimedia.ui.StartMode
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

sealed interface PlayerUiState {
    data object Loading : PlayerUiState
    data class AskResume(val resumeMs: Long) : PlayerUiState
    data object Playing : PlayerUiState
    data class Error(val message: String) : PlayerUiState
}

/**
 * Owns the ExoPlayer for one item: asks the server how to play it (PLAN.md
 * 4.1), plays it directly with sidecar subtitles, applies the server's audio
 * and subtitle choice, and reports progress (4.5).
 */
@OptIn(UnstableApi::class)
class PlayerViewModel(app: Application) : AndroidViewModel(app) {
    private val graph = app.graph

    /** Set by start(): the player prepared ahead for this item (Preloader), or a new one. */
    lateinit var player: ExoPlayer
        private set

    var state by mutableStateOf<PlayerUiState>(PlayerUiState.Loading)
        private set
    var info by mutableStateOf<PlaybackInfo?>(null)
        private set
    var detail by mutableStateOf<ItemDetail?>(null)
        private set
    var warning by mutableStateOf<String?>(null)
        private set
    var ended by mutableStateOf(false)
        private set

    /** Counts seeks, so a seek back into an intro shows "Skip Intro" again. */
    var seeks by mutableStateOf(0)
        private set

    fun clearWarning() {
        warning = null
    }

    private var itemId = ""
    private var startMode = StartMode.ASK
    private var started = false
    /** When playback was started, for the time-to-playing log line. */
    private var startedAt = 0L
    /** When the last seek was requested, for the seek-time log line. */
    private var seekStartedAt = 0L
    private var retried = false
    private var tracksApplied = false
    private var progressJob: Job? = null

    private val listener = object : Player.Listener {
            override fun onTracksChanged(tracks: Tracks) {
                if (!tracksApplied && !tracks.groups.isEmpty()) {
                    tracksApplied = true
                    applyTracks(tracks)
                }
            }

            override fun onIsPlayingChanged(isPlaying: Boolean) {
                if (isPlaying && startedAt > 0) {
                    android.util.Log.i("MiniMedia", "playing ${SystemClock.elapsedRealtime() - startedAt} ms after starting (${info?.mode}${if (wasPreloaded) ", preloaded" else ""})")
                    startedAt = 0
                }
                if (!isPlaying && !player.playWhenReady && player.playbackState == Player.STATE_READY) report(paused = true)
            }

            override fun onPositionDiscontinuity(old: Player.PositionInfo, new: Player.PositionInfo, reason: Int) {
                if (reason == Player.DISCONTINUITY_REASON_SEEK) {
                    seeks++
                    report(paused = !player.playWhenReady)
                }
            }

            override fun onPlaybackStateChanged(playbackState: Int) {
                if (playbackState == Player.STATE_READY && seekStartedAt > 0) {
                    android.util.Log.i("MiniMedia", "seek ready in ${SystemClock.elapsedRealtime() - seekStartedAt} ms (${info?.mode})")
                    seekStartedAt = 0
                }
                if (playbackState == Player.STATE_ENDED) {
                    report(paused = true, atEnd = true)
                    ended = true
                }
            }

            override fun onPlayerError(error: PlaybackException) = handleError(error)

    }

    /** Decision and stream the Preloader prepared, used by the first load(). */
    private var preloaded: Preloader.Prepared? = null
    private var wasPreloaded = false
    /** URL the player currently has prepared, to reuse it instead of preparing again. */
    private var preparedUrl: String? = null

    fun start(id: String, mode: String) {
        if (started) return
        started = true
        val pre = graph.preloader.take(id)
        player = pre?.player ?: buildPlayer(getApplication())
        player.addListener(listener)
        preloaded = pre
        wasPreloaded = pre != null
        preparedUrl = pre?.info?.url
        itemId = id
        startMode = mode
        viewModelScope.launch { load() }
    }

    private suspend fun load(forceConvert: Boolean = false, at: Long? = null) {
        state = PlayerUiState.Loading
        val session = graph.requireSession()
        // Title, show and chapters: fetched alongside, not before, the playback decision.
        if (detail == null) detail = graph.details.item(itemId)
        if (detail == null) viewModelScope.launch { runCatching { session.api.item(itemId) }.onSuccess { detail = it } }
        val pre = preloaded?.takeIf { !forceConvert && at == null }
        preloaded = null
        val pi = pre?.info ?: run {
            // A track chosen in the panel (or the one that just failed) is kept.
            val caps = Capabilities.detect(getApplication()).copy(forceConvert = forceConvert, audioTrack = audioIndex)
            runCatching { session.api.playback(itemId, caps) }.getOrElse {
                state = PlayerUiState.Error("Could not start playback: ${it.message}")
                return
            }
        }
        info = pi
        pi.unsupportedReason?.let {
            state = PlayerUiState.Error(it)
            return
        }
        warning = pi.warnings.firstOrNull()
        when {
            at != null -> begin(at)
            startMode == StartMode.ASK && pi.resumeMs > 0 -> state = PlayerUiState.AskResume(pi.resumeMs)
            startMode == StartMode.RESUME -> begin(pi.resumeMs)
            else -> begin(0)
        }
    }

    fun begin(positionMs: Long) {
        val pi = info ?: return
        val session = graph.requireSession()
        tracksApplied = false
        startedAt = SystemClock.elapsedRealtime()
        startTrickplay(pi)
        if (preparedUrl == pi.url && player.playbackState != Player.STATE_IDLE) {
            // Prepared ahead (or the same stream again): just start, moving only if needed.
            player.setSeekParameters(SeekParameters.EXACT)
            if (kotlin.math.abs(player.currentPosition - positionMs) > 1500) player.seekTo(positionMs)
            if (!player.currentTracks.isEmpty) {
                tracksApplied = true
                applyTracks(player.currentTracks)
            }
        } else {
            player.setMediaItem(mediaItemFor(session, pi, itemId), positionMs)
            player.prepare()
            preparedUrl = pi.url
        }
        player.playWhenReady = true
        state = PlayerUiState.Playing
        progressJob?.cancel()
        progressJob = viewModelScope.launch {
            while (true) {
                delay(10_000)
                if (player.isPlaying) report(paused = false)
            }
        }
    }

    // ---- tracks (PLAN.md 8.4 "Audio & Subtitles panel") ----

    /** Stream index of the audio track in use, and the subtitle id shown (null = off). */
    var audioIndex by mutableStateOf<Int?>(null)
        private set
    var subtitleId by mutableStateOf<String?>(null)
        private set

    /** Applies the server's default audio track and subtitle (PLAN.md 4.1). */
    private fun applyTracks(tracks: Tracks) {
        val pi = info ?: return
        if (audioIndex == null) audioIndex = pi.audioIndex
        if (subtitleId == null && !subtitleChosen) subtitleId = pi.selectedSubtitleId
        val params = player.trackSelectionParameters.buildUpon()
        audioIndex?.let { audioOverride(tracks, it, params) }
        subtitleOverride(tracks, subtitleId, params)
        player.trackSelectionParameters = params.build()
    }

    private var subtitleChosen = false

    // The server lists audio tracks in container order, as ExoPlayer does.
    private fun audioOverride(tracks: Tracks, streamIndex: Int, params: androidx.media3.common.TrackSelectionParameters.Builder) {
        val pi = info ?: return
        val groups = tracks.groups.filter { it.type == C.TRACK_TYPE_AUDIO }
        val k = pi.audioTracks.indexOfFirst { it.index == streamIndex }
        if (k >= 0 && groups.size == pi.audioTracks.size) {
            params.setOverrideForType(TrackSelectionOverride(groups[k].mediaTrackGroup, 0))
        }
    }

    private fun subtitleOverride(tracks: Tracks, id: String?, params: androidx.media3.common.TrackSelectionParameters.Builder) {
        val pi = info ?: return
        val selected = pi.subtitles.firstOrNull { it.id == id }
        if (selected == null) {
            params.setTrackTypeDisabled(C.TRACK_TYPE_TEXT, true)
            return
        }
        params.setTrackTypeDisabled(C.TRACK_TYPE_TEXT, false)
        val textGroups = tracks.groups.filter { it.type == C.TRACK_TYPE_TEXT }
        // Tracks with a url are side-loaded files; the rest play from the container.
        val sidecarIds = pi.subtitles.filter { it.url != null }.map { it.id }
        fun idOf(g: Tracks.Group) = g.getTrackFormat(0).id.orEmpty()
        val group = if (selected.url != null) {
            textGroups.firstOrNull { idOf(it).contains(selected.id) }
        } else {
            val inContainer = textGroups.filter { g -> sidecarIds.none { idOf(g).contains(it) } }
            inContainer.getOrNull(pi.subtitles.filter { it.url == null }.indexOf(selected))
        } ?: textGroups.firstOrNull { it.getTrackFormat(0).language?.startsWith(selected.language) == true }
        group?.let { params.setOverrideForType(TrackSelectionOverride(it.mediaTrackGroup, 0)) }
    }

    /**
     * Switches audio. In direct play a playable track switches in place. A
     * track that needs converting, or any switch while the server converts
     * (one HLS rendition per track), starts a new playback session at the
     * current position (PLAN.md 8.4).
     */
    fun selectAudio(streamIndex: Int) {
        val pi = info ?: return
        val t = pi.audioTracks.firstOrNull { it.index == streamIndex } ?: return
        if (streamIndex == audioIndex) return
        audioIndex = streamIndex
        if (pi.mode == "hls" || !t.playable) {
            savePrefs()
            val at = player.currentPosition
            retried = false
            if (audioDisabled) {
                audioDisabled = false
                player.trackSelectionParameters = player.trackSelectionParameters.buildUpon()
                    .setTrackTypeDisabled(C.TRACK_TYPE_AUDIO, false).build()
            }
            viewModelScope.launch { load(at = at) }
            return
        }
        val params = player.trackSelectionParameters.buildUpon()
        audioOverride(player.currentTracks, streamIndex, params)
        player.trackSelectionParameters = params.build()
        savePrefs()
    }

    fun selectSubtitle(id: String?) {
        subtitleChosen = true
        subtitleId = id
        val params = player.trackSelectionParameters.buildUpon()
        subtitleOverride(player.currentTracks, id, params)
        player.trackSelectionParameters = params.build()
        savePrefs()
    }

    /** Remembers the choice for the show (PUT /api/shows/{id}/prefs). */
    private fun savePrefs() {
        val showId = detail?.item?.showId ?: return
        val pi = info ?: return
        val audioLang = pi.audioTracks.firstOrNull { it.index == audioIndex }?.language.orEmpty()
        val sub = pi.subtitles.firstOrNull { it.id == subtitleId }
        val prefs = io.github.acip.minimedia.data.ShowPrefs(
            audioLanguage = audioLang.takeIf { it != "und" }.orEmpty(),
            subtitleLanguage = sub?.language.orEmpty(),
            subtitlesMode = when {
                sub == null -> "off"
                sub.forced -> "forced_only"
                else -> "on"
            },
        )
        val api = graph.session?.api ?: return
        graph.appScope.launch { runCatching { api.putPrefs(showId, prefs) } }
    }

    // ---- speed ----

    var speed by mutableStateOf(1f)
        private set

    /** PLAN.md 8.4: 0.5–2×, pitch preserved (Media3 keeps pitch at 1 by default). */
    fun changeSpeed(s: Float) {
        speed = s
        player.setPlaybackSpeed(s)
    }

    // ---- trickplay ----

    var trickplay by mutableStateOf<TrickplayLoader?>(null)
        private set

    private fun startTrickplay(pi: PlaybackInfo) {
        val ref = pi.trickplay ?: return
        if (trickplay != null) return
        trickplay = TrickplayLoader(graph.requireSession(), graph.trickplayHttp, graph.json, viewModelScope).also { it.start(ref) }
    }

    // ---- episodes of the current season (player "Episodes" panel) ----

    var seasonEpisodes by mutableStateOf<List<io.github.acip.minimedia.data.Media>>(emptyList())
        private set

    fun loadSeason() {
        val m = detail?.item ?: return
        val showId = m.showId ?: return
        if (seasonEpisodes.isNotEmpty()) return
        viewModelScope.launch {
            runCatching { graph.requireSession().api.show(showId) }.onSuccess { d ->
                seasonEpisodes = d.seasons.firstOrNull { it.season == m.season }?.episodes.orEmpty()
            }
        }
    }

    private var audioDisabled = false

    private fun handleError(error: PlaybackException) {
        val at = player.currentPosition
        if (!retried) {
            // PLAN.md 8.4: retry once, asking the server to convert.
            retried = true
            viewModelScope.launch { load(forceConvert = true, at = at) }
            return
        }
        // The audio decoder still fails (e.g. a decoder the device lists but
        // can't run): keep watching without sound rather than not at all.
        val failed = (error as? ExoPlaybackException)
            ?.takeIf { it.type == ExoPlaybackException.TYPE_RENDERER }
            ?.rendererFormat?.sampleMimeType
        if (!audioDisabled && failed != null && MimeTypes.isAudio(failed)) {
            audioDisabled = true
            warning = "This device couldn't decode the ${codecName(failed)} audio, so the video plays without sound."
            player.trackSelectionParameters = player.trackSelectionParameters.buildUpon()
                .setTrackTypeDisabled(C.TRACK_TYPE_AUDIO, true).build()
            player.prepare()
            player.setSeekParameters(SeekParameters.EXACT)
            player.seekTo(at)
            player.playWhenReady = true
            return
        }
        progressJob?.cancel()
        state = PlayerUiState.Error(readable(error))
    }

    private fun codecName(mime: String?): String = when (mime) {
        MimeTypes.AUDIO_AC3 -> "AC3"
        MimeTypes.AUDIO_E_AC3, MimeTypes.AUDIO_E_AC3_JOC -> "E-AC3"
        MimeTypes.AUDIO_DTS, MimeTypes.AUDIO_DTS_HD, MimeTypes.AUDIO_DTS_EXPRESS -> "DTS"
        MimeTypes.AUDIO_TRUEHD -> "TrueHD"
        MimeTypes.VIDEO_AV1 -> "AV1"
        MimeTypes.VIDEO_H265 -> "HEVC"
        MimeTypes.VIDEO_H264 -> "H.264"
        MimeTypes.VIDEO_VP9 -> "VP9"
        null -> "this"
        else -> mime.substringAfter('/').uppercase()
    }

    private fun readable(e: PlaybackException): String {
        val format = (e as? ExoPlaybackException)?.takeIf { it.type == ExoPlaybackException.TYPE_RENDERER }?.rendererFormat?.sampleMimeType
        return when (e.errorCode) {
            PlaybackException.ERROR_CODE_DECODER_INIT_FAILED,
            PlaybackException.ERROR_CODE_DECODER_QUERY_FAILED,
            PlaybackException.ERROR_CODE_DECODING_FAILED,
            PlaybackException.ERROR_CODE_DECODING_FORMAT_UNSUPPORTED,
            PlaybackException.ERROR_CODE_DECODING_FORMAT_EXCEEDS_CAPABILITIES,
            -> "This file's ${codecName(format)} ${if (format != null && MimeTypes.isAudio(format)) "audio" else "video"} " +
                "format isn't supported by this device."
            PlaybackException.ERROR_CODE_IO_NETWORK_CONNECTION_FAILED,
            PlaybackException.ERROR_CODE_IO_NETWORK_CONNECTION_TIMEOUT,
            -> "Lost the connection to the server."
            PlaybackException.ERROR_CODE_IO_BAD_HTTP_STATUS,
            PlaybackException.ERROR_CODE_IO_FILE_NOT_FOUND,
            -> "The server could not read this file. It may have been moved or deleted."
            else -> "Playback failed: ${e.errorCodeName}"
        }
    }

    /** Sends the position (PLAN.md 4.5). atEnd reports the full duration so the item counts as watched. */
    fun report(paused: Boolean, final: Boolean = false, atEnd: Boolean = false) {
        if (state != PlayerUiState.Playing || itemId.isEmpty()) return
        val duration = player.duration.takeIf { it > 0 } ?: info?.durationMs ?: 0
        val pos = if (atEnd) duration else player.currentPosition
        val api = graph.session?.api ?: return
        val scope: CoroutineScope = if (final) graph.appScope else viewModelScope
        val id = itemId
        scope.launch { runCatching { api.progress(id, ProgressReport(pos, duration, paused)) } }
    }

    fun togglePause() {
        if (player.playbackState == Player.STATE_ENDED) player.seekTo(0)
        player.playWhenReady = !player.playWhenReady
    }

    fun seekBy(deltaMs: Long) = seekTo(player.currentPosition + deltaMs)

    /**
     * fast: land on the keyframe nearest to ms (a scrub, where the user
     * picked a point by eye): no decoding of the frames between the keyframe
     * and ms, so a far seek shows the picture much sooner. Otherwise exact.
     */
    fun seekTo(ms: Long, fast: Boolean = false) {
        player.setSeekParameters(if (fast) SeekParameters.CLOSEST_SYNC else SeekParameters.EXACT)
        seekStartedAt = SystemClock.elapsedRealtime()
        val d = player.duration.takeIf { it > 0 } ?: Long.MAX_VALUE
        player.seekTo(ms.coerceIn(0, maxOf(d - 1000, 0)))
    }

    /** The app went to the background (Home button): pause and save. */
    fun onStop() {
        if (state == PlayerUiState.Playing) {
            player.pause()
            report(paused = true, final = true)
        }
    }

    override fun onCleared() {
        report(paused = true, final = true)
        progressJob?.cancel()
        if (::player.isInitialized) player.release()
    }
}
