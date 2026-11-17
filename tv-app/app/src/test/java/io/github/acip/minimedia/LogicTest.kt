package io.github.acip.minimedia

import io.github.acip.minimedia.data.AppGraph
import io.github.acip.minimedia.data.Discovery
import io.github.acip.minimedia.data.DiscoveryReply
import io.github.acip.minimedia.data.Media
import io.github.acip.minimedia.util.episodeCode
import io.github.acip.minimedia.util.formatClock
import io.github.acip.minimedia.util.formatRuntime
import io.github.acip.minimedia.util.progressFraction
import io.github.acip.minimedia.util.remainingMs
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import java.net.InetAddress

class LogicTest {
    @Test
    fun normalizeUrl() {
        assertEquals("http://192.168.1.20:8096/", AppGraph.normalizeUrl("192.168.1.20"))
        assertEquals("http://192.168.1.20:8098/", AppGraph.normalizeUrl(" 192.168.1.20:8098 "))
        assertEquals("http://nas.local:8096/", AppGraph.normalizeUrl("http://nas.local:8096/api/info"))
        assertNull(AppGraph.normalizeUrl(""))
        assertNull(AppGraph.normalizeUrl("::::"))
    }

    @Test
    fun formatting() {
        assertEquals("1h 52m", formatRuntime(112 * 60_000L))
        assertEquals("45m", formatRuntime(45 * 60_000L))
        assertEquals("2h", formatRuntime(120 * 60_000L))
        assertEquals("", formatRuntime(0))
        assertEquals("1:12:03", formatClock(4_323_000))
        assertEquals("12:03", formatClock(723_000))
        assertEquals("0:05", formatClock(5_400))
        assertEquals("0:00", formatClock(-10))
    }

    @Test
    fun episodeCodes() {
        val e = Media(id = "x", kind = "episode", season = 1, episode = 2)
        assertEquals("S1 · E2", episodeCode(e))
        assertEquals("S1 · E3–4", episodeCode(e.copy(episode = 3, episodeEnd = 4)))
        assertEquals("Special 1", episodeCode(e.copy(season = 0, episode = 1)))
        assertEquals("", episodeCode(Media(id = "m", kind = "movie")))
    }

    @Test
    fun progress() {
        val m = Media(id = "m", kind = "movie", runtimeMs = 100_000, positionMs = 25_000)
        assertEquals(0.25f, progressFraction(m)!!, 0.001f)
        assertEquals(75_000, remainingMs(m))
        assertNull(progressFraction(m.copy(watched = true)))
        assertEquals(100_000, remainingMs(m.copy(watched = true)))
    }

    @Test
    fun discoveryLoopbackReplyUsesSender() {
        val sender = InetAddress.getByName("10.0.2.2")
        val r = DiscoveryReply(name = "S", serverId = "id", url = "http://127.0.0.1:8098")
        assertEquals("http://10.0.2.2:8098", Discovery.fixLoopback(r, sender).url)
        val lan = r.copy(url = "http://192.168.1.20:8096")
        assertEquals(lan, Discovery.fixLoopback(lan, sender))
    }
}
