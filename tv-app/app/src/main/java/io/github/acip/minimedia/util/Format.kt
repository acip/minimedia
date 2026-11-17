package io.github.acip.minimedia.util

import android.content.Context
import android.text.format.DateFormat
import io.github.acip.minimedia.data.Media
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

/** "1h 52m", "45m". */
fun formatRuntime(ms: Long): String {
    val totalMin = ((ms + 30_000) / 60_000).toInt()
    if (totalMin <= 0) return ""
    val h = totalMin / 60
    val m = totalMin % 60
    return when {
        h == 0 -> "${m}m"
        m == 0 -> "${h}h"
        else -> "${h}h ${m}m"
    }
}

/** "1:12:03", "12:03", "0:05". */
fun formatClock(ms: Long): String {
    val total = (maxOf(ms, 0) / 1000).toInt()
    val h = total / 3600
    val m = (total / 60) % 60
    val s = total % 60
    return if (h > 0) String.format(Locale.ROOT, "%d:%02d:%02d", h, m, s)
    else String.format(Locale.ROOT, "%d:%02d", m, s)
}

/**
 * "Ends at 22:49" (PLAN.md 8.4): now + remaining time at the playback speed,
 * in the device's 12/24-hour format.
 */
fun endsAt(context: Context, remainingMs: Long, speed: Float = 1f, now: Long = System.currentTimeMillis()): String {
    val end = now + (maxOf(remainingMs, 0) / maxOf(speed, 0.1f)).toLong()
    val pattern = if (DateFormat.is24HourFormat(context)) "HH:mm" else "h:mm a"
    return SimpleDateFormat(pattern, Locale.getDefault()).format(Date(end))
}

/** "S1 · E2", "S1 · E3–4", "Special 1". */
fun episodeCode(m: Media): String {
    val s = m.season ?: return ""
    val e = m.episode ?: return ""
    val ep = if (m.episodeEnd != null && m.episodeEnd > e) "$e–${m.episodeEnd}" else "$e"
    return if (s == 0) "Special $ep" else "S$s · E$ep"
}

/** Remaining time of an item from its saved position. */
fun remainingMs(m: Media): Long =
    if (m.positionMs > 0 && !m.watched) m.runtimeMs - m.positionMs else m.runtimeMs

/** Fraction watched for progress bars, or null when there is nothing to show. */
fun progressFraction(m: Media): Float? =
    if (m.positionMs > 0 && m.runtimeMs > 0 && !m.watched) (m.positionMs.toFloat() / m.runtimeMs).coerceIn(0f, 1f) else null
