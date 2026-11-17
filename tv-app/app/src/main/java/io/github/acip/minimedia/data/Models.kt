package io.github.acip.minimedia.data

import kotlinx.serialization.Serializable

// JSON shapes of the server API (PLAN.md section 6). Unknown fields are ignored.

@Serializable
data class ServerInfo(
    val name: String = "",
    val version: String = "",
    val serverId: String = "",
    val os: String = "",
)

@Serializable
data class DiscoveryReply(
    val name: String = "",
    val serverId: String = "",
    val url: String = "",
    val version: String = "",
)

/** A movie, an episode or a show, as used in rows, grids and lists. */
@Serializable
data class Media(
    val id: String,
    val kind: String, // movie | episode | show
    val title: String = "",
    val year: Int = 0,
    val showId: String? = null,
    val showTitle: String? = null,
    val season: Int? = null,
    val episode: Int? = null,
    val episodeEnd: Int? = null,
    val episodeTitle: String? = null,
    val runtimeMs: Long = 0,
    val positionMs: Long = 0,
    val watched: Boolean = false,
    val posterUrl: String? = null,
    val backdropUrl: String? = null,
    val logoUrl: String? = null,
    val stillUrl: String? = null,
    val addedAt: Long = 0,
    // shows (and hero items)
    val genres: List<String> = emptyList(),
    val seasonCount: Int = 0,
    val episodeCount: Int = 0,
    val unwatchedCount: Int = 0,
    val overview: String = "",
) {
    val isShow get() = kind == "show"
    val isEpisode get() = kind == "episode"
}

@Serializable
data class HomeResponse(
    val hero: List<Media> = emptyList(),
    val continueWatching: List<Media> = emptyList(),
    val nextUp: List<Media> = emptyList(),
    val recentlyAddedMovies: List<Media> = emptyList(),
    val recentlyAddedEpisodes: List<Media> = emptyList(),
    val movies: List<Media> = emptyList(),
    val shows: List<Media> = emptyList(),
)

@Serializable
data class Season(val season: Int, val title: String = "", val episodes: List<Media> = emptyList())

@Serializable
data class ShowDetail(
    val show: Media,
    val overview: String = "",
    val rating: Double? = null,
    val seasons: List<Season> = emptyList(),
    val nextUpId: String? = null,
)

@Serializable
data class AudioTrack(
    val index: Int,
    val codec: String = "",
    val channels: Int = 0,
    val language: String = "",
    val title: String = "",
    val label: String = "",
    val default: Boolean = false,
    val playable: Boolean = true,
    val selected: Boolean = false,
)

@Serializable
data class SubtitleTrack(
    val id: String,
    val language: String = "",
    val label: String = "",
    val forced: Boolean = false,
    val sdh: Boolean = false,
    val embedded: Boolean = false,
    val streamIndex: Int? = null,
    val codec: String = "",
    val url: String? = null, // set when served as a file (sidecars; embedded text tracks in HLS mode)
    val mime: String = "",
    val selected: Boolean = false,
    val available: Boolean = true, // false: can't be shown in this playback mode
)

@Serializable
data class Marker(val kind: String, val startMs: Long, val endMs: Long)

@Serializable
data class ProgressState(
    val positionMs: Long = 0,
    val durationMs: Long = 0,
    val watched: Boolean = false,
    val playCount: Int = 0,
    val lastPlayedAt: Long? = null,
)

@Serializable
data class ItemDetail(
    val item: Media,
    val overview: String = "",
    val genres: List<String> = emptyList(),
    val rating: Double? = null,
    val durationMs: Long = 0,
    val audioTracks: List<AudioTrack> = emptyList(),
    val subtitles: List<SubtitleTrack> = emptyList(),
    val markers: List<Marker> = emptyList(),
    val nextUpAtMs: Long = 0,
    val progress: ProgressState = ProgressState(),
    val nextEpisode: Media? = null,
)

/** Device capabilities sent with a playback request (PLAN.md 4.1 / 8.5). */
@Serializable
data class PlaybackRequest(
    val videoCodecs: List<String> = emptyList(),
    val hdr: List<String> = emptyList(),
    val audioDecoders: List<String> = emptyList(),
    val audioPassthrough: List<String> = emptyList(),
    val maxChannels: Int = 2,
    val audioTrack: Int? = null,
    val forceConvert: Boolean = false,
)

@Serializable
data class PlaybackInfo(
    val mode: String = "direct", // direct | hls (audio converted on the server, PLAN.md 4.3)
    val url: String = "",
    val convertedAudio: String? = null,
    val container: String = "",
    val audioIndex: Int? = null,
    val audioTracks: List<AudioTrack> = emptyList(),
    val subtitles: List<SubtitleTrack> = emptyList(),
    val selectedSubtitleId: String? = null,
    val markers: List<Marker> = emptyList(),
    val nextUpAtMs: Long = 0,
    val resumeMs: Long = 0,
    val durationMs: Long = 0,
    val unsupportedReason: String? = null,
    val warnings: List<String> = emptyList(),
    val trickplay: TrickplayRef? = null,
)

@Serializable
data class TrickplayRef(val indexUrl: String, val version: String = "")

/** index.json of seek thumbnails (PLAN.md 5.1). */
@Serializable
data class TrickplayIndex(
    val intervalMs: Long,
    val tileWidth: Int,
    val tileHeight: Int,
    val columns: Int = 5,
    val rows: Int = 5,
    val count: Int,
    val sheets: Int,
    val version: String = "",
)

/** Remembered audio/subtitle choice of a show (PLAN.md 4.1, 8.4). */
@Serializable
data class ShowPrefs(
    val audioLanguage: String = "",
    val subtitleLanguage: String = "",
    val subtitlesMode: String = "off", // off | on | forced_only
)

@Serializable
data class ProgressReport(val positionMs: Long, val durationMs: Long, val paused: Boolean)

@Serializable
data class SearchResult(
    val movies: List<Media> = emptyList(),
    val shows: List<Media> = emptyList(),
    val episodes: List<Media> = emptyList(),
)
