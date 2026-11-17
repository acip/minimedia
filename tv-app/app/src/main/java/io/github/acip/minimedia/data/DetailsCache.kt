package io.github.acip.minimedia.data

import android.content.Context
import coil3.SingletonImageLoader
import coil3.request.ImageRequest
import io.github.acip.minimedia.ui.backdropRequest
import kotlinx.coroutines.launch
import java.util.concurrent.ConcurrentHashMap

/**
 * Details fetched ahead of time. While a card has focus, what opening it
 * would show (details, backdrop, logo) is fetched in the background, so the
 * details page has everything on its first frame. Screens still refresh from
 * the server when shown; this only removes the wait.
 */
class DetailsCache(private val context: Context, private val graph: AppGraph) {
    private val items = ConcurrentHashMap<String, ItemDetail>()
    private val shows = ConcurrentHashMap<String, ShowDetail>()
    private val inFlight: MutableSet<String> = ConcurrentHashMap.newKeySet()

    fun item(id: String): ItemDetail? = items[id]
    fun show(id: String): ShowDetail? = shows[id]

    fun put(d: ItemDetail) {
        if (items.size > 300) items.clear()
        items[d.item.id] = d
    }

    fun put(d: ShowDetail) {
        if (shows.size > 100) shows.clear()
        shows[d.show.id] = d
    }

    /** Fetches what opening m shows: its details and artwork. */
    fun prefetch(m: Media) {
        val session = graph.session ?: return
        val api = session.api
        val showId = if (m.isShow) m.id else if (m.isEpisode) m.showId else null
        val key = showId?.let { "show:$it" } ?: "item:${m.id}"
        if (!inFlight.add(key)) return
        graph.appScope.launch {
            try {
                val (backdrop, logo) = if (showId != null) {
                    val d = api.show(showId).also { put(it) }
                    d.show.backdropUrl to d.show.logoUrl
                } else {
                    val d = api.item(m.id).also { put(it) }
                    d.item.backdropUrl to d.item.logoUrl
                }
                val loader = SingletonImageLoader.get(context)
                session.image(backdrop, 1280)?.let { loader.enqueue(backdropRequest(context, it)) }
                session.image(logo, 780)?.let { loader.enqueue(ImageRequest.Builder(context).data(it).build()) }
            } catch (_: Exception) {
                // Only a head start; the screen loads it anyway.
            } finally {
                inFlight.remove(key)
            }
        }
    }
}
