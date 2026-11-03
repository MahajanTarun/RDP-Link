package com.eminentailabs.officerdp

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.graphics.Bitmap
import android.graphics.PixelFormat
import android.hardware.display.DisplayManager
import android.hardware.display.VirtualDisplay
import android.media.ImageReader
import android.media.projection.MediaProjection
import android.media.projection.MediaProjectionManager
import android.os.Build
import android.os.Handler
import android.os.HandlerThread
import android.os.IBinder
import fi.iki.elonen.NanoHTTPD
import org.json.JSONArray
import org.json.JSONObject
import android.os.Looper
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.io.File
import java.io.FileInputStream
import java.net.HttpURLConnection
import java.net.NetworkInterface
import java.net.URL
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference
import java.util.zip.CRC32

class HostService : Service() {
    companion object {
        const val ACTION_STOP = "com.eminentailabs.officerdp.STOP"
        const val EXTRA_PORT = "port"
        const val DEFAULT_PORT = 8088
        const val VERSION = "0.2.0"
        const val SESSION_COOKIE = "rdplink_sid"
        const val SESSION_TTL_MS = 12L * 60 * 60 * 1000
    }

    private var projection: MediaProjection? = null
    private var virtualDisplay: VirtualDisplay? = null
    private var imageReader: ImageReader? = null
    private var captureThread: HandlerThread? = null
    private val latestFrame = AtomicReference<ByteArray?>(null)
    private val latestJpeg = AtomicReference<ByteArray?>(null)
    private val frameEtag = AtomicReference("")
    private val frameWidth = AtomicInteger(0)
    private val frameHeight = AtomicInteger(0)
    // The captured frame is kept as a bitmap and encoded on demand: encoding
    // a full screen PNG on every frame cost hundreds of milliseconds of CPU
    // and was never used by the viewers.
    private val frameBitmap = AtomicReference<android.graphics.Bitmap?>(null)
    private val frameLock = Any()
    private var paddedBitmap: Bitmap? = null
    private var croppedBitmap: Bitmap? = null
    private var cropCanvas: android.graphics.Canvas? = null
    private var server: HostServer? = null
    private var heartbeat: java.util.concurrent.ScheduledExecutorService? = null
    private var peerId: String = ""

    override fun onCreate() {
        super.onCreate()
        peerId = PeerIdentity.get(this)
        startForegroundCompat(notification())
        val port = getSharedPreferences("officerdp", MODE_PRIVATE)
            .getInt("port", DEFAULT_PORT)
        server = HostServer(this, latestFrame, latestJpeg, frameEtag,
            frameWidth, frameHeight, peerId, port)
        try {
            server?.start(NanoHTTPD.SOCKET_READ_TIMEOUT, false)
        } catch (e: Exception) {
            audit("server start failed: ${e.message}")
        }
        startHeartbeat()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            stopSelf()
            return START_NOT_STICKY
        }
        val resultCode = intent?.getIntExtra("resultCode", 0) ?: 0
        val data = intent?.getParcelableExtra<Intent>("data")
        if (resultCode != 0 && data != null && projection == null) {
            val mpm = getSystemService(Context.MEDIA_PROJECTION_SERVICE) as MediaProjectionManager
            projection = mpm.getMediaProjection(resultCode, data)
            startCapture()
        }
        return START_STICKY
    }

    private fun startCapture() {
        val metrics = resources.displayMetrics
        val width = metrics.widthPixels
        val height = metrics.heightPixels
        val density = metrics.densityDpi
        frameWidth.set(width)
        frameHeight.set(height)

        captureThread = HandlerThread("officerdp-capture").also { it.start() }
        val handler = Handler(captureThread!!.looper)

        // Required on Android 14+: the callback must be registered before
        // createVirtualDisplay, otherwise MediaProjection throws.
        projection?.registerCallback(object : MediaProjection.Callback() {
            override fun onStop() {
                audit("media projection stopped by system")
                stopSelf()
            }
        }, handler)

        imageReader = ImageReader.newInstance(width, height, PixelFormat.RGBA_8888, 2)
        virtualDisplay = projection?.createVirtualDisplay(
            "OfficeRDP", width, height, density,
            DisplayManager.VIRTUAL_DISPLAY_FLAG_AUTO_MIRROR,
            imageReader?.surface, null, handler
        )
        imageReader?.setOnImageAvailableListener({ reader ->
            val image = reader.acquireLatestImage() ?: return@setOnImageAvailableListener
            try {
                val plane = image.planes[0]
                val buffer = plane.buffer
                val pixelStride = plane.pixelStride
                val rowStride = plane.rowStride
                val rowPadding = rowStride - pixelStride * width
                val paddedWidth = width + rowPadding / pixelStride
                // The bitmaps are allocated once and reused, so a frame does
                // not allocate ten megabytes of garbage every 90 ms.
                synchronized(frameLock) {
                    // The bitmaps are allocated once and reused, so a frame
                    // does not allocate ten megabytes of garbage.
                    var padded = paddedBitmap
                    if (padded == null || padded.width != paddedWidth) {
                        padded = Bitmap.createBitmap(paddedWidth, height, Bitmap.Config.ARGB_8888)
                        paddedBitmap = padded
                        croppedBitmap = null
                        cropCanvas = null
                    }
                    var cropped = croppedBitmap
                    if (cropped == null) {
                        cropped = Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888)
                        croppedBitmap = cropped
                        cropCanvas = android.graphics.Canvas(cropped)
                    }
                    padded.copyPixelsFromBuffer(buffer)
                    cropCanvas!!.drawBitmap(padded, 0f, 0f, null)
                    val jpeg = ByteArrayOutputStream()
                    cropped.compress(Bitmap.CompressFormat.JPEG, 70, jpeg)
                    val bytes = jpeg.toByteArray()
                    val crc = CRC32()
                    crc.update(bytes)
                    latestJpeg.set(bytes)
                    frameEtag.set("\"j${java.lang.Long.toHexString(crc.value)}\"")
                    frameBitmap.set(cropped)
                    latestFrame.set(null)
                }
            } catch (e: Exception) {
                audit("frame encode failed: ${e.message}")
            } finally {
                image.close()
            }
        }, handler)
        audit("capture started ${width}x${height}")
    }

    override fun onDestroy() {
        heartbeat?.shutdownNow()
        heartbeat = null
        server?.stop()
        server = null
        virtualDisplay?.release()
        imageReader?.close()
        projection?.stop()
        captureThread?.quitSafely()
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    private fun startForegroundCompat(n: Notification) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            startForeground(1, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PROJECTION)
        } else {
            startForeground(1, n)
        }
    }

    private fun notification(): Notification {
        val channelId = "officerdp"
        val nm = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        nm.createNotificationChannel(
            NotificationChannel(channelId, "RDP Link host", NotificationManager.IMPORTANCE_LOW)
        )
        val open = PendingIntent.getActivity(
            this, 0, Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        )
        val stop = PendingIntent.getService(
            this, 1, Intent(this, HostService::class.java).setAction(ACTION_STOP),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        )
        return Notification.Builder(this, channelId)
            .setContentTitle("RDP Link — sharing screen")
            .setContentText("Peer ID $peerId • tap to open")
            .setSmallIcon(android.R.drawable.ic_menu_share)
            .setContentIntent(open)
            .addAction(Notification.Action.Builder(null, "Stop", stop).build())
            .setAutoCancel(false)
            .setOngoing(true)
            .setCategory(Notification.CATEGORY_SERVICE)
            .build()
    }

    private fun startHeartbeat() {
        val prefs = getSharedPreferences("officerdp", MODE_PRIVATE)
        val rendezvous = prefs.getString("rendezvous", "")?.trim().orEmpty()
        if (rendezvous.isEmpty()) return
        val port = prefs.getInt("port", DEFAULT_PORT)
        val alias = prefs.getString("alias", "Android") ?: "Android"
        heartbeat = Executors.newSingleThreadScheduledExecutor()
        val task = Runnable { register(rendezvous, alias, port) }
        heartbeat?.scheduleWithFixedDelay(task, 0, 60, TimeUnit.SECONDS)
    }

    private fun register(rendezvous: String, alias: String, port: Int) {
        try {
            val addr = localIp()?.let { "$it:$port" } ?: ":$port"
            val body = JSONObject()
                .put("peerId", peerId.replace(" ", ""))
                .put("alias", alias)
                .put("status", "online")
                .put("addr", addr)
                .put("caps", JSONObject().put("host", true).put("input", true).put("platform", "android"))
            val text = body.toString()
            val url = rendezvous.trimEnd('/') + "/register"
            val conn = URL(url).openConnection() as HttpURLConnection
            conn.requestMethod = "POST"
            conn.connectTimeout = 5000
            conn.readTimeout = 5000
            conn.doOutput = true
            conn.setRequestProperty("Content-Type", "application/json")
            // Signed with the device key: the rendezvous only accepts a
            // registration that proves ownership of the peer ID.
            try {
                val headers = JSONObject(
                    mobile.Mobile.signRequest(filesDir.absolutePath, "POST", url, text)
                )
                headers.keys().forEach { key ->
                    conn.setRequestProperty(key, headers.getString(key))
                }
            } catch (e: Throwable) {
                audit("sign failed: ${e.message}")
            }
            conn.outputStream.use { it.write(text.toByteArray()) }
            val code = conn.responseCode
            conn.disconnect()
            audit("rendezvous register: $code")
        } catch (e: Exception) {
            audit("rendezvous register failed: ${e.message}")
        }
    }

    private fun localIp(): String? {
        return try {
            NetworkInterface.getNetworkInterfaces().toList()
                .flatMap { it.inetAddresses.toList() }
                .firstOrNull { !it.isLoopbackAddress && it is java.net.Inet4Address }
                ?.hostAddress
        } catch (e: Exception) {
            null
        }
    }

    /** Encodes the current frame as PNG. Rarely used, so it is done on demand. */
    fun encodeFramePng(): ByteArray? {
        synchronized(frameLock) {
            val bitmap = frameBitmap.get() ?: return null
            return try {
                val out = ByteArrayOutputStream()
                bitmap.compress(Bitmap.CompressFormat.PNG, 100, out)
                out.toByteArray()
            } catch (e: Exception) {
                null
            }
        }
    }

    private fun audit(message: String) {
        android.util.Log.i("OfficeRDP", message)
        try {
            getSharedPreferences("officerdp", MODE_PRIVATE).edit()
                .putString("lastAudit", "${System.currentTimeMillis()} $message")
                .apply()
        } catch (_: Exception) {
        }
    }

    class HostServer(
        private val service: HostService,
        private val frameRef: AtomicReference<ByteArray?>,
        private val jpegRef: AtomicReference<ByteArray?>,
        private val etagRef: AtomicReference<String>,
        private val frameWidth: AtomicInteger,
        private val frameHeight: AtomicInteger,
        private val peerId: String,
        port: Int
    ) : NanoHTTPD(port) {
        private val sessions = ConcurrentHashMap<String, Long>()
        private val password: String
            get() = service.getSharedPreferences("officerdp", MODE_PRIVATE)
                .getString("password", "") ?: ""

        private fun constantEquals(a: String, b: String): Boolean {
            if (a.length != b.length) return false
            var diff = 0
            for (i in a.indices) diff = diff or (a[i].code xor b[i].code)
            return diff == 0
        }

        private fun sessionToken(session: IHTTPSession): String? {
            session.parameters["token"]?.firstOrNull()?.let { return it }
            val cookie = session.headers["cookie"] ?: return null
            return cookie.split(";").map { it.trim() }
                .firstOrNull { it.startsWith("$SESSION_COOKIE=") }
                ?.substringAfter("=")
        }

        private fun isValidSession(token: String): Boolean {
            if (token.isEmpty()) return false
            val expiry = sessions[token] ?: return false
            if (expiry < System.currentTimeMillis()) {
                sessions.remove(token)
                return false
            }
            return true
        }

        // authorized accepts the access password as a header, or a session
        // token as a cookie or query parameter. The password itself is never
        // accepted from a URL.
        private fun authorized(session: IHTTPSession): Boolean {
            val expected = password
            if (expected.isEmpty()) return false
            session.headers["x-office-password"]?.let {
                return constantEquals(expected, it)
            }
            val token = sessionToken(session)
            return token != null && isValidSession(token)
        }

        private fun handleSession(session: IHTTPSession): Response {
            if (session.method != NanoHTTPD.Method.POST) {
                return newFixedLengthResponse(
                    Response.Status.METHOD_NOT_ALLOWED, "text/plain", "method"
                )
            }
            val expected = password
            val supplied = session.headers["x-office-password"]
                ?: session.parameters["password"]?.firstOrNull()
                ?: bodyText(session).takeIf { it.isNotEmpty() }?.let {
                    try {
                        JSONObject(it).optString("password")
                    } catch (_: Exception) {
                        ""
                    }
                }
                ?: ""
            if (expected.isEmpty() || !constantEquals(expected, supplied)) {
                return newFixedLengthResponse(
                    Response.Status.UNAUTHORIZED, "text/plain", "unauthorized"
                )
            }
            val token = java.util.UUID.randomUUID().toString().replace("-", "")
            sessions[token] = System.currentTimeMillis() + SESSION_TTL_MS
            val response = newFixedLengthResponse(
                Response.Status.OK, "application/json",
                JSONObject().put("token", token).toString()
            )
            response.addHeader(
                "Set-Cookie",
                "$SESSION_COOKIE=$token; Path=/; HttpOnly; SameSite=Strict; Max-Age=43200"
            )
            return response
        }

        override fun serve(session: IHTTPSession): Response {
            val uri = session.uri
            if (uri == "/session") return handleSession(session)
            if (uri == "/logo.png") {
                val asset = service.assets.open("logo.png")
                val bytes = asset.readBytes()
                asset.close()
                return newFixedLengthResponse(
                    Response.Status.OK, "image/png",
                    ByteArrayInputStream(bytes), bytes.size.toLong()
                )
            }
            if (uri == "/" || uri == "/index.html") return serveViewer()
            if (!authorized(session)) {
                return newFixedLengthResponse(
                    Response.Status.UNAUTHORIZED, "text/plain", "unauthorized"
                )
            }
            if (uri == "/info") {
                val prefs = service.getSharedPreferences("officerdp", MODE_PRIVATE)
                val alias = prefs.getString("alias", "Android") ?: "Android"
                val port = prefs.getInt("port", DEFAULT_PORT)
                val body = JSONObject()
                    .put("peerId", peerId)
                    .put("alias", alias)
                    .put("status", "online")
                    .put("port", port)
                    .put("version", VERSION)
                    .put("platform", "android")
                return newFixedLengthResponse(
                    Response.Status.OK, "application/json", body.toString()
                )
            }
            return when (uri) {
                "/frame.png" -> {
                    // PNG is encoded on demand: most guests only need JPEG.
                    val bytes = frameRef.get() ?: encodePng()
                    if (bytes == null) noFrame()
                    else newFixedLengthResponse(
                        Response.Status.OK, "image/png",
                        ByteArrayInputStream(bytes), bytes.size.toLong()
                    )
                }
                "/frame.jpg" -> {
                    val bytes = jpegRef.get()
                    if (bytes == null) noFrame()
                    else {
                        val etag = etagRef.get()
                        val inm = session.headers["if-none-match"]
                        if (etag.isNotEmpty() && inm == etag) {
                            newFixedLengthResponse(
                                Response.Status.NOT_MODIFIED, "image/jpeg", ""
                            )
                        } else {
                            val response = newFixedLengthResponse(
                                Response.Status.OK, "image/jpeg",
                                ByteArrayInputStream(bytes), bytes.size.toLong()
                            )
                            response.addHeader("ETag", etag)
                            response.addHeader("Cache-Control", "no-store")
                            response
                        }
                    }
                }
                "/input" -> handleInput(session)
                "/clipboard" -> handleClipboard(session)
                "/files" -> handleFiles()
                "/download" -> handleDownload(session)
                "/upload" -> handleUpload(session)
                "/health" -> newFixedLengthResponse(
                    Response.Status.OK, "application/json", "{\"status\":\"ok\"}"
                )
                else -> newFixedLengthResponse(
                    Response.Status.NOT_FOUND, "text/plain", "not found"
                )
            }
        }

        private fun noFrame(): Response = newFixedLengthResponse(
            Response.Status.SERVICE_UNAVAILABLE, "text/plain", "no frame"
        )

        /** The guest UI is the same page the desktop host serves. */
        private fun serveViewer(): Response {
            return try {
                val asset = service.assets.open("viewer.html")
                val bytes = asset.readBytes()
                asset.close()
                newFixedLengthResponse(
                    Response.Status.OK, "text/html; charset=utf-8",
                    ByteArrayInputStream(bytes), bytes.size.toLong()
                )
            } catch (e: Exception) {
                newFixedLengthResponse(
                    Response.Status.NOT_FOUND, "text/plain", "no viewer"
                )
            }
        }

        private fun encodePng(): ByteArray? = service.encodeFramePng()


        // -- clipboard and file transfer (parity with the desktop host) ------
        private val inbox: File
            get() = File(service.filesDir, "inbox").apply { mkdirs() }

        private fun bodyText(session: IHTTPSession): String {
            val map = HashMap<String, String>()
            try {
                session.parseBody(map)
            } catch (_: Exception) {
            }
            return map["postData"] ?: ""
        }

        private fun handleClipboard(session: IHTTPSession): Response {
            if (session.method == NanoHTTPD.Method.POST) {
                val text = try {
                    JSONObject(bodyText(session)).optString("text")
                } catch (_: Exception) {
                    ""
                }
                // The clipboard must be touched on the main thread.
                Handler(Looper.getMainLooper()).post {
                    val cm = service.getSystemService(ClipboardManager::class.java)
                    cm.setPrimaryClip(ClipData.newPlainText("RDP Link", text))
                }
                return newFixedLengthResponse(Response.Status.NO_CONTENT, "text/plain", "")
            }
            var text = readClipboard()
            if (text.isEmpty()) {
                // The OS refuses clipboard reads for background apps; the
                // accessibility service keeps the last value.
                text = InputAccessibilityService.instance?.clipboardCache().orEmpty()
            }
            return newFixedLengthResponse(
                Response.Status.OK, "application/json",
                JSONObject().put("text", text).toString()
            )
        }

        /** Reads the clipboard on the main thread and waits for the result. */
        private fun readClipboard(): String {
            val latch = java.util.concurrent.CountDownLatch(1)
            val holder = arrayOfNulls<String>(1)
            Handler(Looper.getMainLooper()).post {
                try {
                    val cm = service.getSystemService(ClipboardManager::class.java)
                    holder[0] = cm.primaryClip
                        ?.takeIf { it.itemCount > 0 }
                        ?.getItemAt(0)
                        ?.coerceToText(service)
                        ?.toString() ?: ""
                } catch (_: Exception) {
                    holder[0] = ""
                }
                latch.countDown()
            }
            latch.await(1, TimeUnit.SECONDS)
            return holder[0] ?: ""
        }

        private fun handleFiles(): Response {
            val names = inbox.listFiles()
                ?.filter { it.isFile }
                ?.map { it.name }
                ?.sorted()
                ?: emptyList()
            return newFixedLengthResponse(
                Response.Status.OK, "application/json", JSONArray(names).toString()
            )
        }

        private fun handleDownload(session: IHTTPSession): Response {
            val raw = session.parameters["name"]?.firstOrNull() ?: ""
            val file = File(inbox, File(raw).name)
            if (!file.isFile) {
                return newFixedLengthResponse(
                    Response.Status.NOT_FOUND, "text/plain", "not found"
                )
            }
            val response = newFixedLengthResponse(
                Response.Status.OK, "application/octet-stream",
                FileInputStream(file), file.length()
            )
            // Downloads must not render inline; strip quotes from the name so
            // the header stays valid.
            response.addHeader(
                "Content-Disposition",
                "attachment; filename=\"${file.name.replace("\"", "")}\""
            )
            return response
        }

        private fun handleUpload(session: IHTTPSession): Response {
            val length = session.headers["content-length"]?.toLongOrNull() ?: 0L
            if (length > 256L * 1024 * 1024) {
                return newFixedLengthResponse(
                    Response.Status.PAYLOAD_TOO_LARGE, "text/plain", "too large"
                )
            }
            val files = HashMap<String, String>()
            try {
                session.parseBody(files)
            } catch (_: Exception) {
            }
            val tmp = files.values.firstOrNull { it.isNotEmpty() }
            val requested = session.parameters["name"]?.firstOrNull()
                ?.takeIf { it.isNotEmpty() }
                ?: "upload.bin"
            val target = File(inbox, File(requested).name)
            if (tmp != null && File(tmp).isFile) {
                File(tmp).copyTo(target, overwrite = true)
            } else {
                val raw = files["postData"]
                if (raw != null) target.writeBytes(raw.toByteArray())
            }
            return newFixedLengthResponse(Response.Status.NO_CONTENT, "text/plain", "")
        }

        private fun handleInput(session: IHTTPSession): Response {
            val body = HashMap<String, String>()
            try {
                session.parseBody(body)
            } catch (_: Exception) {
            }
            val raw = body["postData"] ?: ""
            val ev = try {
                JSONObject(raw)
            } catch (_: Exception) {
                JSONObject()
            }
            val a11y = InputAccessibilityService.instance
            val type = ev.optString("type")
            val width = frameWidth.get().coerceAtLeast(1)
            val height = frameHeight.get().coerceAtLeast(1)
            val x = ev.optDouble("x", 0.0).toFloat().let { if (it <= 1f) it * width else it }
            val y = ev.optDouble("y", 0.0).toFloat().let { if (it <= 1f) it * height else it }
            when (type) {
                "move" -> Unit
                "click", "down", "up" -> a11y?.tap(x, y)
                "key" -> a11y?.key(ev.optString("key"))
                "text" -> a11y?.appendText(ev.optString("text"))
                "wheel" -> a11y?.scroll(ev.optInt("delta", 1))
                else -> Unit
            }
            return newFixedLengthResponse(Response.Status.NO_CONTENT, "text/plain", "")
        }
    }
}
