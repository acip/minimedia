package io.github.acip.minimedia.ui.player

import android.graphics.BitmapFactory
import android.util.LruCache
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.IntSize
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import io.github.acip.minimedia.data.Session
import io.github.acip.minimedia.data.TrickplayIndex
import io.github.acip.minimedia.data.TrickplayRef
import io.github.acip.minimedia.util.formatClock
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json
import okhttp3.OkHttpClient
import okhttp3.Request
import kotlin.math.roundToInt

/**
 * Seek thumbnails (PLAN.md 8.4 "Trickplay in the app"): fetches index.json,
 * prefetches every sheet into the HTTP disk cache, and keeps the 6 most
 * recently used sheets decoded. Tiles are drawn from the sheet with a source
 * rectangle, never copied.
 */
class TrickplayLoader(
    private val session: Session,
    private val http: OkHttpClient,
    private val json: Json,
    private val scope: CoroutineScope,
) {
    var index by mutableStateOf<TrickplayIndex?>(null)
        private set

    /** Bumped when a sheet finishes decoding, so thumbnails recompose. */
    var revision by mutableIntStateOf(0)
        private set

    private var base = ""
    private val decoded = LruCache<Int, ImageBitmap>(6)
    private val loading = mutableSetOf<Int>()

    fun start(ref: TrickplayRef) = scope.launch(Dispatchers.IO) {
        base = ref.indexUrl.substringBefore("index.json")
        val idx = runCatching {
            http.newCall(Request.Builder().url(session.url(ref.indexUrl)).build()).execute().use { r ->
                if (!r.isSuccessful) null else json.decodeFromString<TrickplayIndex>(r.body.string())
            }
        }.getOrNull() ?: return@launch
        withContext(Dispatchers.Main) { index = idx }
        // Prefetch everything in the background (cheap: ~100 KB per sheet).
        for (n in 0 until idx.sheets) {
            runCatching { http.newCall(request(n, idx)).execute().use { it.body.bytes() } }
        }
    }

    private fun request(n: Int, idx: TrickplayIndex) =
        Request.Builder().url(session.url("$base$n.jpg?v=${idx.version}")).build()

    /** The sheet and source rectangle of the thumbnail nearest positionMs, if decoded. */
    fun tile(positionMs: Long): Pair<ImageBitmap, Pair<IntOffset, IntSize>>? {
        val idx = index ?: return null
        val i = (positionMs.toDouble() / idx.intervalMs).roundToInt().coerceIn(0, idx.count - 1)
        val perSheet = idx.columns * idx.rows
        val sheet = i / perSheet
        val bmp = decoded.get(sheet) ?: run { load(sheet, idx); return null }
        val k = i % perSheet
        val offset = IntOffset((k % idx.columns) * idx.tileWidth, (k / idx.columns) * idx.tileHeight)
        return bmp to (offset to IntSize(idx.tileWidth, idx.tileHeight))
    }

    private fun load(sheet: Int, idx: TrickplayIndex) {
        synchronized(loading) { if (!loading.add(sheet)) return }
        scope.launch(Dispatchers.IO) {
            val bmp = runCatching {
                http.newCall(request(sheet, idx)).execute().use { r ->
                    val bytes = r.body.bytes()
                    BitmapFactory.decodeByteArray(bytes, 0, bytes.size, BitmapFactory.Options().apply { inSampleSize = 1 })
                        ?.asImageBitmap()
                }
            }.getOrNull()
            withContext(Dispatchers.Main) {
                if (bmp != null) decoded.put(sheet, bmp)
                synchronized(loading) { loading.remove(sheet) }
                revision++
            }
        }
    }
}

/** The thumbnail and target time shown above the scrub head. */
@Composable
fun ScrubPreview(loader: TrickplayLoader?, positionMs: Long, width: Dp, modifier: Modifier = Modifier) {
    val idx = loader?.index
    @Suppress("UNUSED_VARIABLE") val rev = loader?.revision // recompose when a sheet arrives
    Column(modifier, horizontalAlignment = Alignment.CenterHorizontally) {
        val t = loader?.tile(positionMs)
        if (idx != null) {
            Box(
                Modifier.width(width).aspectRatio(idx.tileWidth.toFloat() / idx.tileHeight)
                    .clip(RoundedCornerShape(6.dp)).border(2.dp, Color.White, RoundedCornerShape(6.dp))
                    .background(Color(0xFF111111)),
            ) {
                if (t != null) {
                    Canvas(Modifier.matchParentSize()) {
                        drawImage(
                            t.first, srcOffset = t.second.first, srcSize = t.second.second,
                            dstSize = IntSize(size.width.roundToInt(), size.height.roundToInt()),
                        )
                    }
                }
            }
        }
        Text(
            formatClock(positionMs),
            style = MaterialTheme.typography.titleMedium,
            modifier = Modifier.padding(top = 6.dp).background(Color(0xAA000000), RoundedCornerShape(4.dp))
                .padding(horizontal = 8.dp, vertical = 2.dp),
        )
    }
}
