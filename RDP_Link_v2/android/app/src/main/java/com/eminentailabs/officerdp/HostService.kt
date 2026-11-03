package com.eminentailabs.officerdp

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
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
import org.json.JSONObject
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.net.HttpURLConnection
import java.net.NetworkInterface
import java.net.URL
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference

class HostService : Service() {
    companion object {
        const val ACTION_STOP = "com.eminentailabs.officerdp.STOP"
        const val EXTRA_PORT = "port"
        const val DEFAULT_PORT = 8088
        const val VERSION = "0.2.0"
    }

    private var projection: MediaProjection? = null
    private var virtualDisplay: VirtualDisplay? = null
    private var imageReader: ImageReader? = null
    private var captureThread: HandlerThread? = null
    private val latestFrame = AtomicReference<ByteArray?>(null)
    private val latestJpeg = AtomicReference<ByteArray?>(null)
    private val frameWidth = AtomicInteger(0)
    private val frameHeight = AtomicInteger(0)
    private var server: HostServer? = null
    private var heartbeat: java.util.concurrent.ScheduledExecutorService? = null
    private var peerId: String = ""

    override fun onCreate() {
        super.onCreate()
        peerId = PeerIdentity.get(this)
        startForegroundCompat(notification())
        val port = getSharedPreferences("officerdp", MODE_PRIVATE)
            .getInt("port", DEFAULT_PORT)
        server = HostServer(this, latestFrame, latestJpeg, frameWidth, frameHeight, peerId, port)
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
                val bmp = Bitmap.createBitmap(paddedWidth, height, Bitmap.Config.ARGB_8888)
                bmp.copyPixelsFromBuffer(buffer)
                val cropped = Bitmap.createBitmap(bmp, 0, 0, width, height)
                val png = ByteArrayOutputStream()
                cropped.compress(Bitmap.CompressFormat.PNG, 100, png)
                latestFrame.set(png.toByteArray())
                val jpeg = ByteArrayOutputStream()
                cropped.compress(Bitmap.CompressFormat.JPEG, 70, jpeg)
                latestJpeg.set(jpeg.toByteArray())
                cropped.recycle()
                bmp.recycle()
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
            NotificationChannel(channelId, "Office RDP host", NotificationManager.IMPORTANCE_LOW)
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
            .setContentTitle("Office RDP — sharing screen")
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
            val url = URL(rendezvous.trimEnd('/') + "/register")
            val conn = url.openConnection() as HttpURLConnection
            conn.requestMethod = "POST"
            conn.connectTimeout = 5000
            conn.readTimeout = 5000
            conn.doOutput = true
            conn.setRequestProperty("Content-Type", "application/json")
            conn.outputStream.use { it.write(body.toString().toByteArray()) }
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
        private val frameWidth: AtomicInteger,
        private val frameHeight: AtomicInteger,
        private val peerId: String,
        port: Int
    ) : NanoHTTPD(port) {
        private val password: String
            get() = service.getSharedPreferences("officerdp", MODE_PRIVATE)
                .getString("password", "") ?: ""

        private fun authorized(session: IHTTPSession): Boolean {
            val expected = password
            if (expected.isEmpty()) return false
            val supplied = session.parameters["password"]?.firstOrNull()
                ?: session.headers["x-office-password"]
                ?: ""
            if (supplied.length != expected.length) return false
            var diff = 0
            for (i in expected.indices) diff = diff or (expected[i].code xor supplied[i].code)
            return diff == 0
        }

        override fun serve(session: IHTTPSession): Response {
            val uri = session.uri
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
            if (!authorized(session)) {
                return newFixedLengthResponse(
                    Response.Status.UNAUTHORIZED, "text/plain", "unauthorized"
                )
            }
            return when (uri) {
                "/frame.png" -> {
                    val bytes = frameRef.get()
                    if (bytes == null) noFrame()
                    else newFixedLengthResponse(
                        Response.Status.OK, "image/png",
                        ByteArrayInputStream(bytes), bytes.size.toLong()
                    )
                }
                "/frame.jpg" -> {
                    val bytes = jpegRef.get()
                    if (bytes == null) noFrame()
                    else newFixedLengthResponse(
                        Response.Status.OK, "image/jpeg",
                        ByteArrayInputStream(bytes), bytes.size.toLong()
                    )
                }
                "/input" -> handleInput(session)
                "/clipboard" -> newFixedLengthResponse(
                    Response.Status.OK, "application/json", "{\"text\":\"\"}"
                )
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
