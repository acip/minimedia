package io.github.acip.minimedia.ui

import android.content.Context
import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.LinearGradient
import android.graphics.Paint
import android.graphics.Rect
import android.graphics.Shader
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.composed
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.graphics.FilterQuality
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.IntSize
import coil3.request.ImageRequest
import coil3.request.transformations
import coil3.size.Size
import coil3.transform.Transformation

/*
 * Gradients are expensive on TV GPUs: on a Chromecast with Google TV the two
 * full-screen backdrop scrims alone took ~40 ms of GPU time per frame, so
 * every focus move rendered at ~10 fps. Instead:
 *  - the backdrop scrims are painted into the backdrop bitmap once, on a
 *    background thread, when the image loads (BackdropScrim), so drawing the
 *    backdrop is a single opaque image;
 *  - smaller fades (navigation bar, player controls) are tiny pre-rendered
 *    bitmaps stretched over just the area they cover (Modifier.verticalFade).
 */

/**
 * Crops a backdrop to the 16:9 screen (keeping the top-right, as the layout
 * shows it) and darkens it: left side for the title and text, bottom for the
 * rows, and a top band for the navigation. Same look as the old scrims.
 */
object BackdropScrim : Transformation() {
    override val cacheKey = "backdrop-scrim-v1"

    override suspend fun transform(input: Bitmap, size: Size): Bitmap {
        val iw = input.width
        val ih = input.height
        // Crop to 16:9, anchored top-right (ContentScale.Crop + Alignment.TopEnd).
        val (cw, ch) = if (iw * 9 > ih * 16) (ih * 16 / 9) to ih else iw to (iw * 9 / 16)
        val src = Rect(iw - cw, 0, iw, ch)
        val out = Bitmap.createBitmap(cw, ch, Bitmap.Config.ARGB_8888)
        val canvas = Canvas(out)
        canvas.drawBitmap(input, src, Rect(0, 0, cw, ch), Paint(Paint.FILTER_BITMAP_FLAG))
        val w = cw.toFloat()
        val h = ch.toFloat()
        val paint = Paint().apply { isDither = true }
        paint.shader = LinearGradient(0f, 0f, w, 0f,
            intArrayOf(0xF0000000.toInt(), 0x99000000.toInt(), 0x00000000), floatArrayOf(0f, 0.45f, 0.8f), Shader.TileMode.CLAMP)
        canvas.drawRect(0f, 0f, w * 0.8f, h, paint)
        paint.shader = LinearGradient(0f, 0f, 0f, h,
            intArrayOf(0x00000000, 0xEE000000.toInt(), 0xFF000000.toInt()), floatArrayOf(0.35f, 0.85f, 1f), Shader.TileMode.CLAMP)
        canvas.drawRect(0f, h * 0.35f, w, h, paint)
        // Top band behind the navigation (it has no background of its own on Home).
        paint.shader = LinearGradient(0f, 0f, 0f, h * 0.2f,
            intArrayOf(0xCC000000.toInt(), 0x99000000.toInt(), 0x00000000), floatArrayOf(0f, 0.5f, 1f), Shader.TileMode.CLAMP)
        canvas.drawRect(0f, 0f, w, h * 0.2f, paint)
        return out
    }
}

/**
 * The request for a full-screen backdrop. Shared by the screens and by
 * prefetching, so both hit the same memory-cache entry (the key includes the
 * size when a transformation is applied).
 */
fun backdropRequest(context: Context, url: String): ImageRequest {
    val dm = context.resources.displayMetrics
    return ImageRequest.Builder(context)
        .data(url)
        .size(dm.widthPixels, dm.heightPixels)
        .transformations(BackdropScrim)
        .build()
}

/** A 4×64 bitmap of a vertical black fade; stops are (position, alpha 0..1). */
private fun fadeBitmap(stops: List<Pair<Float, Float>>): ImageBitmap {
    val h = 64
    val px = IntArray(4 * h)
    for (y in 0 until h) {
        val f = (y + 0.5f) / h
        var a = stops.first().second
        for (i in 1 until stops.size) {
            val (x0, a0) = stops[i - 1]
            val (x1, a1) = stops[i]
            if (f >= x0) a = if (f >= x1) a1 else a0 + (a1 - a0) * (f - x0) / (x1 - x0)
        }
        val v = (a.coerceIn(0f, 1f) * 255f).toInt() shl 24
        for (x in 0 until 4) px[y * 4 + x] = v
    }
    return Bitmap.createBitmap(px, 4, h, Bitmap.Config.ARGB_8888).asImageBitmap()
}

/** Draws a vertical black fade behind the content (cheap: one stretched tiny bitmap). */
fun Modifier.verticalFade(vararg stops: Pair<Float, Float>): Modifier = composed {
    val bmp = remember(*stops) { fadeBitmap(stops.toList()) }
    drawBehind {
        drawImage(
            bmp,
            srcOffset = IntOffset(0, 0),
            srcSize = IntSize(bmp.width, bmp.height),
            dstSize = IntSize(size.width.toInt(), size.height.toInt()),
            filterQuality = FilterQuality.Low,
        )
    }
}
