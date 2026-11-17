package io.github.acip.minimedia.ui

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.path
import androidx.compose.ui.unit.dp

/** The few icons the app needs, drawn here instead of pulling in an icon library. */
object AppIcons {
    private fun icon(name: String, block: androidx.compose.ui.graphics.vector.PathBuilder.() -> Unit) =
        ImageVector.Builder(name, 24.dp, 24.dp, 24f, 24f)
            .path(fill = SolidColor(Color.White), pathBuilder = block)
            .build()

    val Play = icon("play") { moveTo(8f, 5f); lineTo(19f, 12f); lineTo(8f, 19f); close() }
    val Pause = icon("pause") {
        moveTo(6f, 5f); lineTo(10f, 5f); lineTo(10f, 19f); lineTo(6f, 19f); close()
        moveTo(14f, 5f); lineTo(18f, 5f); lineTo(18f, 19f); lineTo(14f, 19f); close()
    }
    val Next = icon("next") {
        moveTo(6f, 5f); lineTo(15f, 12f); lineTo(6f, 19f); close()
        moveTo(16f, 5f); lineTo(19f, 5f); lineTo(19f, 19f); lineTo(16f, 19f); close()
    }
    val Check = icon("check") {
        moveTo(9f, 16.2f); lineTo(4.8f, 12f); lineTo(3.4f, 13.4f); lineTo(9f, 19f)
        lineTo(21f, 7f); lineTo(19.6f, 5.6f); close()
    }
    val Restart = icon("restart") {
        // a circular arrow approximated with straight segments
        moveTo(12f, 5f); lineTo(12f, 1f); lineTo(7f, 6f); lineTo(12f, 11f); lineTo(12f, 7f)
        curveTo(15.3f, 7f, 18f, 9.7f, 18f, 13f); curveTo(18f, 16.3f, 15.3f, 19f, 12f, 19f)
        curveTo(8.7f, 19f, 6f, 16.3f, 6f, 13f); lineTo(4f, 13f)
        curveTo(4f, 17.4f, 7.6f, 21f, 12f, 21f); curveTo(16.4f, 21f, 20f, 17.4f, 20f, 13f)
        curveTo(20f, 8.6f, 16.4f, 5f, 12f, 5f); close()
    }
}
