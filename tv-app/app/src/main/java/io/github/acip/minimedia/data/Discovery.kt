package io.github.acip.minimedia.data

import android.content.Context
import android.net.wifi.WifiManager
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.InetAddress
import java.net.NetworkInterface
import java.net.SocketTimeoutException

/** UDP discovery (PLAN.md 6 "Discovery" and 8.2). */
object Discovery {
    const val PORT = 7360
    private const val PAYLOAD = "MINIMEDIA_DISCOVER_V1"

    /**
     * Broadcasts to 255.255.255.255 and every interface's broadcast address,
     * 3 times over [timeoutMs], and returns the distinct servers that replied.
     */
    suspend fun discover(context: Context, json: Json, timeoutMs: Long = 3000): List<DiscoveryReply> =
        withContext(Dispatchers.IO) {
            val wifi = context.applicationContext.getSystemService(WifiManager::class.java)
            val lock = wifi?.createMulticastLock("minimedia-discovery")?.apply {
                setReferenceCounted(false)
                runCatching { acquire() }
            }
            try {
                DatagramSocket().use { socket ->
                    socket.broadcast = true
                    socket.soTimeout = 250
                    val payload = PAYLOAD.toByteArray()
                    val targets = broadcastAddresses() + InetAddress.getByName("255.255.255.255")
                    val found = LinkedHashMap<String, DiscoveryReply>()
                    val buf = ByteArray(4096)
                    val deadline = System.currentTimeMillis() + timeoutMs
                    var nextSend = 0L
                    var sends = 0
                    while (System.currentTimeMillis() < deadline) {
                        val now = System.currentTimeMillis()
                        if (sends < 3 && now >= nextSend) {
                            for (t in targets) {
                                runCatching { socket.send(DatagramPacket(payload, payload.size, t, PORT)) }
                            }
                            sends++
                            nextSend = now + timeoutMs / 3
                        }
                        try {
                            val p = DatagramPacket(buf, buf.size)
                            socket.receive(p)
                            val text = String(p.data, p.offset, p.length, Charsets.UTF_8)
                            val reply = runCatching { json.decodeFromString<DiscoveryReply>(text) }.getOrNull()
                            if (reply != null && reply.serverId.isNotEmpty() && reply.url.isNotEmpty()) {
                                found[reply.serverId] = fixLoopback(reply, p.address)
                            }
                        } catch (_: SocketTimeoutException) {
                        }
                    }
                    found.values.toList()
                }
            } finally {
                runCatching { lock?.release() }
            }
        }

    /**
     * A server that saw the request arrive over loopback (the Android emulator's
     * NAT delivers it that way) advertises 127.0.0.1, which on this device is
     * the device itself. Use the address the reply came from instead.
     */
    internal fun fixLoopback(reply: DiscoveryReply, sender: InetAddress): DiscoveryReply {
        val uri = runCatching { java.net.URI(reply.url) }.getOrNull() ?: return reply
        val host = uri.host ?: return reply
        val loopback = host == "localhost" || host.startsWith("127.") || host == "0.0.0.0" || host == "::1" || host == "[::1]"
        if (!loopback) return reply
        val port = if (uri.port > 0) uri.port else 8096
        return reply.copy(url = "${uri.scheme}://${sender.hostAddress}:$port")
    }

    private fun broadcastAddresses(): List<InetAddress> = runCatching {
        NetworkInterface.getNetworkInterfaces().toList()
            .filter { it.isUp && !it.isLoopback }
            .flatMap { it.interfaceAddresses }
            .mapNotNull { it.broadcast }
            .distinct()
    }.getOrDefault(emptyList())
}
