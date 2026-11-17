# MiniMedia

A small home media server for Windows and a Google TV app. Point the server at
your movie and TV folders; the TV app finds it on your network and plays
everything with a Netflix-style player: seek thumbnails, Skip Intro, next
episode, audio and subtitle choices remembered per show, and "Ends at" times.

- The server only **reads** your media folders. It never renames, moves,
  writes or deletes anything in them.
- Video is never re-encoded. Audio your TV can't play (DTS, TrueHD, or Dolby
  Digital Plus on a TV that only takes AC3) is converted on the fly while the
  video is copied as-is, so it's fast and the picture is untouched.
- Optional TMDB metadata: posters, backdrops, logos, descriptions, in your
  language.

`PLAN.md` is the full specification.

---

## 1. Install the server

### What you need
- Windows 10 or 11 (64-bit).
- **ffmpeg** (a "full" build, which includes the HDR tone-mapping filter used
  for thumbnails). Either:
  - `winget install Gyan.FFmpeg`, or
  - download `ffmpeg-release-full.7z` from <https://www.gyan.dev/ffmpeg/builds/>
    and put `ffmpeg.exe` and `ffprobe.exe` next to `minimedia.exe`.

  MiniMedia looks for them next to `minimedia.exe`, then at the paths in the
  config, then on `PATH`.

### Get minimedia.exe
Build it from this repository (needs Go 1.27+; the TV app part needs Android
Studio's JDK and SDK):

```powershell
.\build.ps1            # dist\minimedia.exe and dist\MiniMedia-tv.apk
.\build.ps1 -SkipApp   # just the server
```

Put `minimedia.exe` in a folder of its own, e.g. `C:\MiniMedia`.

### First run
1. Run `minimedia.exe`. With no config it writes a default `config.yaml` next to
   itself (if that folder is writable; otherwise in
   `%ProgramData%\MiniMedia\`) and tells you where.
2. Edit `config.yaml`: add your folders under `libraries`. Every setting is
   explained in the file. The minimum is:

   ```yaml
   libraries:
     - name: Movies
       type: movies
       path: 'D:\Media\Movies'
     - name: Shows
       type: shows
       path: '\\nas\media\Shows'
   tmdb_api_key: "your key"        # optional, free at themoviedb.org → Settings → API
   metadata_language: ro-RO        # optional
   subtitles:
     preferred_languages: [ro, en] # subtitle and audio languages, in order
   ```

   Backslash paths need single quotes. Typos in setting names are reported at
   startup with the line number.
3. Run `minimedia.exe` again. It scans the folders (new files are picked up
   while it runs), then fetches metadata and builds seek thumbnails in the
   background.

The server takes no command-line arguments; everything is in `config.yaml`.
Config changes need a restart; media changes don't.

### Allow it through the firewall
The first time it runs, Windows asks whether to allow MiniMedia on networks.
Tick **Private networks** and allow. If you missed the prompt, run this in an
administrator PowerShell (adjust the path):

```powershell
New-NetFirewallRule -DisplayName "MiniMedia" -Direction Inbound -Program "C:\MiniMedia\minimedia.exe" -Action Allow -Profile Private
```

Your home network must be set to **Private** (Settings → Network & internet →
your Wi-Fi or Ethernet → Network profile type). On a **Public** network the
firewall blocks the TV, and MiniMedia logs a warning about it at startup.

### Start it with Windows (optional)
MiniMedia is a normal program, not a Windows service. To start it when you log
in, put a shortcut to `minimedia.exe` in your Startup folder (press Win+R, type
`shell:startup`). In the shortcut's properties you can set **Run: Minimized**.
It keeps running while you're logged in, including when the screen is locked.

### While it runs
- The PC won't go to sleep while the TV is playing (from the first stream
  request until 2 minutes after the last). The display may still turn off.
- ffmpeg processes it starts are stopped when MiniMedia exits, even if it is
  killed or crashes.
- Close it with Ctrl+C (or by closing the window).
- Set `log_file` in `config.yaml` to also keep a log file. It's rotated at
  10 MB, with 3 old files kept.

---

## 2. Install the TV app (Google TV / Android TV)

The app isn't on the Play Store; you install `MiniMedia-tv.apk` once with
Android's `adb` tool (part of Android Studio, or "SDK Platform Tools" from
<https://developer.android.com/tools/releases/platform-tools>).

1. On the TV: **Settings → System → About**, scroll to **Android TV OS build**
   and press OK seven times until it says you are a developer.
2. **Settings → System → Developer options**: turn on **USB debugging**, or on
   newer devices (Chromecast with Google TV) **Wireless debugging**.
3. On the PC, connect:
   - Wireless debugging: open **Wireless debugging → Pair device with pairing
     code** on the TV, then

     ```powershell
     adb pair 192.168.1.50:41234 123456   # address and code shown on the TV
     adb connect 192.168.1.50:37000       # the address on the Wireless debugging screen
     ```

     (`adb mdns services` lists both addresses while the TV shows them.)
   - USB debugging (older devices): `adb connect 192.168.1.50:5555` and accept
     the prompt on the TV.
4. Install (and later update) the app, open it once, then have Android compile
   it ahead of time. Otherwise the TV does that on its own some time later
   (usually overnight), and until then the menus are less smooth.

   ```powershell
   adb install -r MiniMedia-tv.apk
   adb shell am start -n io.github.acip.minimedia/.MainActivity
   # wait about 10 seconds, then:
   adb shell cmd package compile -m speed-profile -f io.github.acip.minimedia
   ```

Open **MiniMedia** from the TV's apps. It finds the server automatically. If
it doesn't (see Troubleshooting), enter the PC's address, e.g.
`192.168.1.20:8096`; `ipconfig` shows the PC's IPv4 address.

**Frame rate:** for smooth 24 fps films, turn on the TV's **Match content
frame rate** (Settings → Display & Sound → Advanced display settings). The app
leaves refresh-rate switching to that setting.

---

## 3. Organising your media

### Movies (`type: movies`)
- `Movies\Title (Year)\Title (Year).mkv`, or loose `Title (Year).mkv`, or
  scene names like `Title.2019.1080p.BluRay.x264-GRP.mkv`.
- A movies folder may also contain **TV episodes**, as download folders often
  do: files named with `S01E02` or `1x02` become episodes, grouped into shows by
  the name before the code or by their folder (`Show (2025)\Season 2\…`,
  `Show.S01.1080p.WEB\…`).

### Shows (`type: shows`)
- `Shows\Show Name (Year)\Season 01\Show Name - S01E02 - Title.mkv`.
- `Specials` (or `Season 00`) is season 0. Multi-episode files: `S01E02E03` or
  `S01E02-E03`. A file named only `E05` takes the season from its folder.

Files that can't be parsed are skipped and listed at
`http://<pc>:8096/api/library/unmatched`. Folders named `Extras`, `Trailers`,
`Featurettes`, `Behind The Scenes`, `Deleted Scenes`, `Interviews`, `Samples`
or `Other`, and small `sample` files, are ignored.

### Subtitles
Next to the video, or in a `Subs`/`Subtitles` folder:
`Movie.srt`, `Movie.ro.srt`, `Movie.en.forced.srt`, `Movie.en.sdh.srt`, and
`Subs\Movie\2_English.srt`. `.ass`, `.ssa` and `.vtt` work too. Romanian files
in old encodings (Windows-1250, ISO-8859-2) are converted, and cedilla letters
(ş ţ) become the correct comma-below ones (ș ț).

### Artwork and metadata
- In a movie or show folder: `poster.jpg` / `folder.jpg` / `cover.jpg`,
  `fanart.jpg` / `backdrop.jpg`, `logo.png` / `clearlogo.png`. For movies
  sharing a folder: `<file name>-poster.jpg`. Episode stills:
  `<episode file name>-thumb.jpg`.
- Local pictures win over TMDB; without either, backdrops and stills are
  frames from the video.
- Wrong TMDB match? Put a `tmdb.txt` in the movie or show folder containing
  the TMDB id or URL.

---

## 4. Troubleshooting

**The TV app doesn't find the server.** Check that the PC's network is
**Private** and that the firewall allows MiniMedia (section 1). Discovery uses
UDP port 7360, and the app connects on the configured `port` (8096 by
default). If another program already uses that port, MiniMedia exits with an
error; pick another `port`. A VPN on the PC is fine; on the TV, enter the
address manually if discovery still fails.

**No sound, or "converted for this device".** The TV can't play that audio
format itself, so the server converts it: to E-AC3 5.1, AC3 5.1 or AAC stereo,
whichever the TV supports. That's expected and keeps surround where possible.
If a TV with a soundbar reports less than it supports, check the TV's
**Settings → Display & Sound → Audio → Surround sound**.

**The first play of a big file takes a few seconds.** Before converting audio
the server indexes the file's keyframes. This happens in the background after
a scan, so it's usually ready; otherwise it takes roughly as long as reading
the file once.

**A subtitle is "unavailable with converted audio".** Picture-based subtitles
(PGS, VobSub) can only be shown when the file plays directly; with converted
audio, choose a text subtitle or an external `.srt`.

**Wrong colours on a Dolby Vision (profile 5) file.** These need a Dolby
Vision display; the app warns about it before playing.

**A network share isn't scanned.** Use the UNC path (`\\nas\share\Movies`). If
the share is unreachable when a scan runs, MiniMedia keeps the existing items
instead of removing them. Changes on shares are picked up by the periodic
rescan (`scan.rescan_every_hours`).

**Where are the logs and the database?** The log goes to the console window
(and to `log_file` if set). The database, thumbnails and caches are in
`data_dir` (default `%ProgramData%\MiniMedia\data`). Deleting `data_dir`
starts over (watched state and preferences included). Converted audio is
cached there too: at most 20 GB, and unused files go after 6 hours.

**Status at a glance:** `http://<pc>:8096/api/library/status` shows the scan,
metadata, thumbnail and audio-conversion progress.

---

## Development (Windows)

Requirements: Go 1.27+, ffmpeg/ffprobe (full build) on PATH, and for the app
Android Studio (JDK 17, SDK 37).

```powershell
.\testdata\generate.ps1          # make the sample library in testdata\media (about 20 s)
.\testdata\run-dev.ps1           # build testdata\run\minimedia.exe, write a dev config, run it
```

The dev config uses port **8098** so it can run alongside Jellyfin (8096) and
a WSL service that owns 8097. Edit `testdata\run\config.yaml` to point it at
other folders.

```powershell
cd server
go test ./...                    # unit tests; several also use testdata\media when present
go run ./cmd/discover            # UDP discovery test client (broadcast)
..\testdata\discover.ps1         # the same in PowerShell
curl.exe http://localhost:8098/api/movies
curl.exe -r 0-999 -o part.bin http://localhost:8098/stream/<id>/file

cd ..\tv-app
.\gradlew.bat assembleDebug testDebugUnitTest
```

A debug build runs several times slower on a TV than the release build
(`assembleRelease`, what `build.ps1` ships), so measure UI speed on a release
build. On TV GPUs, avoid full-screen gradients and translucent full-screen
layers: on a Chromecast with Google TV, two of them cost ~40 ms per frame.
`ui/Scrims.kt` explains the alternatives used.

The app keeps actions instant by doing the work ahead of time. Screens stay
built under the one on top (`ui/ScreenStack.kt`), so Back only reveals them.
While focus rests on a card, its details and artwork are fetched
(`data/DetailsCache.kt`). On a details page, or a card that plays, the video
is buffered paused (`ui/player/Preload.kt`), so Play just starts it.

In the Android TV emulator, enter `10.0.2.2:8098` (UDP discovery replies don't
reach the emulator).

### Layout

```
server/      Go module: cmd/minimedia (server), cmd/discover (test client), internal/...
tv-app/      Android TV app (Kotlin, Compose for TV, Media3)
testdata/    generate.ps1, run-dev.ps1, discover.ps1 (generated media is git-ignored)
build.ps1    release build into dist\
```

## License

MIT. See `LICENSE`.
