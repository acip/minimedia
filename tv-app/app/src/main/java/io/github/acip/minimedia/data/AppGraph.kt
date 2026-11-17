package io.github.acip.minimedia.data

import android.content.Context
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.first
import kotlinx.serialization.json.Json
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory
import java.util.concurrent.TimeUnit

private val Context.settingsStore by preferencesDataStore("settings")

/** The server the app is talking to. */
data class Session(val baseUrl: String, val info: ServerInfo, val api: MiniMediaApi) {
    /** Absolute URL of a server path ("/images/…", "/stream/…"). */
    fun url(path: String): String = baseUrl.trimEnd('/') + path

    /** Image URL at one of the server's widths (342, 780, 1280, 1920). */
    fun image(path: String?, width: Int): String? =
        path?.let { url(it) + (if ('?' in it) "&" else "?") + "w=$width" }
}

data class SavedServer(val url: String, val serverId: String, val name: String)

/** App-wide singletons (no DI framework for an app this size). */
class AppGraph(context: Context) {
    private val app = context.applicationContext

    val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        encodeDefaults = true
        coerceInputValues = true
    }

    val http: OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(4, TimeUnit.SECONDS)
        .readTimeout(30, TimeUnit.SECONDS)
        .build()

    /** For work that must outlive a screen, e.g. the final progress report. */
    val appScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    /** Seek-thumbnail sheets are cached on disk (the server sends long cache headers). */
    val trickplayHttp: OkHttpClient = http.newBuilder()
        .cache(okhttp3.Cache(app.cacheDir.resolve("trickplay"), 300L * 1024 * 1024))
        .build()

    val playerSettings = PlayerSettingsStore(app, appScope)

    /** Details and artwork fetched ahead while cards have focus. */
    val details = DetailsCache(app, this)

    /** The next likely playback, buffered ahead (main thread only). */
    val preloader by lazy { io.github.acip.minimedia.ui.player.Preloader(app as android.app.Application, this) }

    /**
     * Episodes played in a row by autoplay without any remote input; at 3 the
     * player asks "Are you still watching?" (PLAN.md 8.4).
     */
    @Volatile
    var autoplayStreak = 0

    @Volatile
    var session: Session? = null
        private set

    fun apiFor(baseUrl: String): MiniMediaApi = Retrofit.Builder()
        .baseUrl(baseUrl)
        .client(http)
        .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
        .build()
        .create(MiniMediaApi::class.java)

    /** Connects to baseUrl ("http://host:port/") and makes it the current session. */
    suspend fun connect(baseUrl: String): Session {
        val api = apiFor(baseUrl)
        val info = api.info()
        return Session(baseUrl, info, api).also { session = it }
    }

    fun requireSession(): Session = session ?: error("not connected to a server")

    // ---- remembered server ----

    private val keyUrl = stringPreferencesKey("server_url")
    private val keyId = stringPreferencesKey("server_id")
    private val keyName = stringPreferencesKey("server_name")

    suspend fun savedServer(): SavedServer? {
        val p = app.settingsStore.data.first()
        val url = p[keyUrl] ?: return null
        return SavedServer(url, p[keyId].orEmpty(), p[keyName].orEmpty())
    }

    suspend fun saveServer(s: Session) {
        app.settingsStore.edit {
            it[keyUrl] = s.baseUrl
            it[keyId] = s.info.serverId
            it[keyName] = s.info.name
        }
    }

    suspend fun forgetServer() {
        app.settingsStore.edit { it.clear() }
        session = null
    }

    companion object {
        /** "192.168.1.20", "192.168.1.20:8096" or a full URL → "http://host:port/". */
        fun normalizeUrl(input: String): String? {
            var s = input.trim()
            if (s.isEmpty()) return null
            if (!s.startsWith("http://") && !s.startsWith("https://")) s = "http://$s"
            val uri = runCatching { java.net.URI(s) }.getOrNull() ?: return null
            val host = uri.host ?: return null
            val port = if (uri.port > 0) uri.port else 8096
            return "${uri.scheme}://$host:$port/"
        }
    }
}
