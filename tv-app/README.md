# MiniMedia for Google TV

Kotlin, Compose for TV and Media3 (PLAN.md section 8). Android TV only
(minSdk 31, targetSdk 37).

## Build and run

Open this folder (`tv-app`) in Android Studio and run the `app` configuration,
or from a terminal:

```powershell
.\gradlew.bat assembleDebug                       # app\build\outputs\apk\debug\app-debug.apk
adb install -r app\build\outputs\apk\debug\app-debug.apk
adb shell am start -n io.github.acip.minimedia/.MainActivity
```

### Emulator

An Android TV emulator can't see UDP broadcasts from your LAN, so on first
start the app falls back to manual entry: type `10.0.2.2:8098` (the dev
server; `10.0.2.2` is the Windows host as seen from the emulator).

### Chromecast with Google TV

1. Settings → System → About → click "Android TV OS build" 7 times.
2. Settings → System → Developer options → enable USB debugging.
3. `adb connect <tv-ip>:5555`, accept the prompt on the TV, then `adb install` as above.

The app finds the server by itself on the same network (UDP port 7360).
