import java.util.Properties

plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
}

// Release signing key: from tv-app/keystore.properties (local, git-ignored) or
// from MINIMEDIA_KEYSTORE* environment variables (GitHub Actions). Without
// either, release builds are signed with the debug key.
val keystoreProps = Properties().apply {
    val f = rootProject.file("keystore.properties")
    if (f.exists()) f.inputStream().use { load(it) }
}
fun signing(prop: String, env: String): String? = keystoreProps.getProperty(prop) ?: System.getenv(env)?.ifEmpty { null }
val releaseStore = signing("storeFile", "MINIMEDIA_KEYSTORE")

// Version: -PappVersion=1.2.3 (release builds from a v1.2.3 tag), else "dev".
// versionCode 1.2.3 -> 1002003, so every release installs over the last one.
val appVersion = (findProperty("appVersion") as String?) ?: "dev"
val appVersionCode = Regex("""^(\d+)\.(\d+)\.(\d+)""").find(appVersion)?.destructured
    ?.let { (major, minor, patch) -> major.toInt() * 1_000_000 + minor.toInt() * 1_000 + patch.toInt() } ?: 1

android {
    namespace = "io.github.acip.minimedia"
    compileSdk = 37

    defaultConfig {
        applicationId = "io.github.acip.minimedia"
        minSdk = 31
        targetSdk = 37
        versionCode = appVersionCode
        versionName = appVersion
    }

    signingConfigs {
        if (releaseStore != null) {
            create("release") {
                storeFile = file(releaseStore)
                storePassword = signing("storePassword", "MINIMEDIA_KEYSTORE_PASSWORD")
                keyAlias = signing("keyAlias", "MINIMEDIA_KEY_ALIAS")
                keyPassword = signing("keyPassword", "MINIMEDIA_KEY_PASSWORD")
            }
        }
    }

    buildTypes {
        release {
            // R8 and no debuggable flag: Compose is several times faster than in a debug build.
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            signingConfig = signingConfigs.getByName(if (releaseStore != null) "release" else "debug")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    buildFeatures {
        compose = true
        buildConfig = true
    }
}

dependencies {
    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.activity.compose)
    implementation(platform(libs.compose.bom))
    implementation(libs.compose.ui)
    implementation(libs.compose.foundation)
    debugImplementation(libs.compose.ui.tooling)
    implementation(libs.tv.material)
    implementation(libs.lifecycle.viewmodel.compose)
    implementation(libs.lifecycle.runtime.compose)
    implementation(libs.datastore.preferences)
    implementation(libs.media3.exoplayer)
    implementation(libs.media3.exoplayer.hls)
    implementation(libs.media3.ui)
    implementation(libs.coil.compose)
    implementation(libs.coil.network.okhttp)
    implementation(libs.okhttp)
    implementation(libs.retrofit)
    implementation(libs.retrofit.kotlinx.serialization)
    implementation(libs.kotlinx.serialization.json)
    implementation(libs.kotlinx.coroutines.android)
    testImplementation(libs.junit)
}
