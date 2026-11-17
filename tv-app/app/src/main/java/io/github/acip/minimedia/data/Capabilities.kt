package io.github.acip.minimedia.data

import android.content.Context
import android.hardware.display.DisplayManager
import android.media.MediaCodecList
import android.view.Display
import androidx.annotation.OptIn
import androidx.media3.common.AudioAttributes
import androidx.media3.common.C
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.audio.AudioCapabilities
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/**
 * What this device can play (PLAN.md 8.5). The audio output is checked before
 * every playback request, since an HDMI receiver may have been switched on or
 * off; the decoder list never changes and is read once (it takes a while on
 * TV hardware). Runs off the main thread.
 */
object Capabilities {
    private val videoTypes = linkedMapOf(
        "video/avc" to "h264",
        "video/hevc" to "hevc",
        "video/x-vnd.on2.vp9" to "vp9",
        "video/av01" to "av1",
        "video/mpeg2" to "mpeg2",
        "video/x-vnd.on2.vp8" to "vp8",
    )
    private val audioTypes = linkedMapOf(
        "audio/mp4a-latm" to "aac",
        "audio/ac3" to "ac3",
        "audio/eac3" to "eac3",
        "audio/opus" to "opus",
        "audio/flac" to "flac",
        "audio/vorbis" to "vorbis",
        "audio/mpeg" to "mp3",
        "audio/vnd.dts" to "dts",
        "audio/true-hd" to "truehd",
    )

    private val decoders: Set<String> by lazy {
        MediaCodecList(MediaCodecList.REGULAR_CODECS).codecInfos
            .filter { !it.isEncoder }
            .flatMap { it.supportedTypes.map { t -> t.lowercase() } }
            .toSet()
    }

    /** Reads the decoder list ahead of the first playback request. */
    fun warm() {
        decoders
    }

    suspend fun detect(context: Context): PlaybackRequest = withContext(Dispatchers.Default) { detectNow(context) }

    @OptIn(UnstableApi::class)
    private fun detectNow(context: Context): PlaybackRequest {
        val video = videoTypes.filterKeys { it in decoders }.values.toList()
        val audio = audioTypes.filterKeys { it in decoders }.values.toList()

        val display = context.getSystemService(DisplayManager::class.java)?.getDisplay(Display.DEFAULT_DISPLAY)
        @Suppress("DEPRECATION")
        val hdrTypes = display?.hdrCapabilities?.supportedHdrTypes ?: IntArray(0)
        val hdr = hdrTypes.toList().mapNotNull {
            when (it) {
                Display.HdrCapabilities.HDR_TYPE_HDR10 -> "hdr10"
                Display.HdrCapabilities.HDR_TYPE_HLG -> "hlg"
                Display.HdrCapabilities.HDR_TYPE_DOLBY_VISION -> "dv"
                Display.HdrCapabilities.HDR_TYPE_HDR10_PLUS -> "hdr10plus"
                else -> null
            }
        }

        // No spatializer channel masks: TVs output to HDMI/speakers, not head-tracked audio.
        val caps = AudioCapabilities.getCapabilities(context, AudioAttributes.DEFAULT, null, emptyList())
        val passthrough = buildList {
            if (caps.supportsEncoding(C.ENCODING_AC3)) add("ac3")
            if (caps.supportsEncoding(C.ENCODING_E_AC3)) add("eac3")
            if (caps.supportsEncoding(C.ENCODING_DTS)) add("dts")
            if (caps.supportsEncoding(C.ENCODING_DOLBY_TRUEHD)) add("truehd")
        }
        return PlaybackRequest(
            videoCodecs = video,
            hdr = hdr,
            audioDecoders = audio,
            audioPassthrough = passthrough,
            maxChannels = maxOf(caps.maxChannelCount, 2),
        )
    }
}
