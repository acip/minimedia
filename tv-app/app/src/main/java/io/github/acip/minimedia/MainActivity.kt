package io.github.acip.minimedia

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import io.github.acip.minimedia.ui.AppNav
import io.github.acip.minimedia.ui.MiniMediaTheme

class MainActivity : ComponentActivity() {
    override fun onStop() {
        super.onStop()
        // In the background, don't hold a decoder (or the server) for a title that may never play.
        graph.preloader.release()
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent {
            MiniMediaTheme {
                AppNav()
            }
        }
    }
}
