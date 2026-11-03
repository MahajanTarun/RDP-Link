package com.eminentailabs.officerdp

import android.Manifest
import android.app.Activity
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.view.accessibility.AccessibilityManager
import android.accessibilityservice.AccessibilityServiceInfo
import android.content.pm.PackageManager
import android.media.projection.MediaProjectionManager
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.provider.Settings
import android.view.View
import android.widget.ArrayAdapter
import android.widget.Button
import android.widget.EditText
import android.widget.ListView
import android.widget.TextView
import android.widget.Toast
import java.net.HttpURLConnection
import java.net.URL
import java.security.SecureRandom
import kotlin.concurrent.thread
import org.json.JSONObject

class MainActivity : Activity() {
    private val REQUEST_CODE = 1001
    private val NOTIFICATION_REQUEST = 1002
    private lateinit var projectionManager: MediaProjectionManager
    private lateinit var peerIdView: TextView
    private lateinit var passwordView: EditText
    private lateinit var portView: EditText
    private lateinit var rendezvousView: EditText
    private lateinit var statusView: TextView
    private lateinit var urlView: EditText
    private lateinit var recentView: ListView
    private lateinit var advancedPanel: View
    private lateinit var advancedToggle: TextView

    private val prefs by lazy { getSharedPreferences("officerdp", Context.MODE_PRIVATE) }

    private var sharing = false
    private val ui = Handler(Looper.getMainLooper())
    private val statusTick = object : Runnable {
        override fun run() {
            refreshRemoteControlStatus()
            ui.postDelayed(this, 2000)
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)
        projectionManager = getSystemService(MEDIA_PROJECTION_SERVICE) as MediaProjectionManager

        peerIdView = findViewById(R.id.peerId)
        passwordView = findViewById(R.id.password)
        portView = findViewById(R.id.port)
        rendezvousView = findViewById(R.id.rendezvous)
        statusView = findViewById(R.id.status)
        urlView = findViewById(R.id.url)
        recentView = findViewById(R.id.recentList)
        advancedPanel = findViewById(R.id.advancedPanel)
        advancedToggle = findViewById(R.id.advancedToggle)
        statusView.setOnClickListener {
            startActivity(Intent(Settings.ACTION_ACCESSIBILITY_SETTINGS))
            toast("Enable RDP Link remote control")
        }

        peerIdView.text = PeerIdentity.get(this)
        passwordView.setText(prefs.getString("password", "") ?: "")
        portView.setText(prefs.getInt("port", HostService.DEFAULT_PORT).toString())
        rendezvousView.setText(prefs.getString("rendezvous", "") ?: "")

        findViewById<Button>(R.id.copyId).setOnClickListener {
            val cm = getSystemService(ClipboardManager::class.java)
            cm.setPrimaryClip(ClipData.newPlainText("RDP Link peer id", peerIdView.text))
            toast("Peer ID copied")
        }
        findViewById<Button>(R.id.genPassword).setOnClickListener {
            passwordView.setText(randomPassword())
            saveSettings()
        }
        findViewById<Button>(R.id.startHost).setOnClickListener {
            saveSettings()
            requestNotificationPermission()
            startActivityForResult(projectionManager.createScreenCaptureIntent(), REQUEST_CODE)
        }
        findViewById<Button>(R.id.stopHost).setOnClickListener {
            sharing = false
            stopService(Intent(this, HostService::class.java))
            setStatus("Sharing stopped")
            refreshRemoteControlStatus()
        }
        findViewById<Button>(R.id.enableA11y).setOnClickListener {
            startActivity(Intent(Settings.ACTION_ACCESSIBILITY_SETTINGS))
            toast("Enable RDP Link remote control")
        }

        advancedToggle.setOnClickListener {
            val open = advancedPanel.visibility != View.VISIBLE
            advancedPanel.visibility = if (open) View.VISIBLE else View.GONE
            advancedToggle.text = if (open) "Advanced settings ▴" else "Advanced settings ▾"
        }

        findViewById<Button>(R.id.go).setOnClickListener {
            openViewer(urlView.text.toString().trim())
        }

        recentView.setOnItemClickListener { _, _, position, _ ->
            val target = recentItems().getOrNull(position) ?: return@setOnItemClickListener
            urlView.setText(target)
            openViewer(target)
        }
        recentView.setOnItemLongClickListener { _, _, position, _ ->
            val items = recentItems().toMutableList()
            if (position in items.indices) {
                val removed = items.removeAt(position)
                saveRecent(items)
                refreshRecent()
                toast("Removed $removed")
            }
            true
        }
        refreshRecent()

        intent?.getStringExtra("rendezvous")?.let { relay ->
            rendezvousView.setText(relay)
            saveSettings()
        }

        // Test/deployment helper: adb ... --es seed "host1:8088,host2:8088"
        intent?.getStringExtra("seed")?.let { seed ->
            val items = recentItems().toMutableList()
            seed.split(',').map { it.trim() }.filter { it.isNotEmpty() }.forEach { entry ->
                items.remove(entry)
                items.add(0, entry)
            }
            saveRecent(items)
            refreshRecent()
        }

        if (prefs.getString("password", "").isNullOrEmpty()) {
            passwordView.setText(randomPassword())
            saveSettings()
        }
    }

    // -- recents -------------------------------------------------------------
    private fun recentItems(): List<String> =
        prefs.getString("recent", "")
            ?.split('\n')
            ?.filter { it.isNotBlank() }
            ?: emptyList()

    private fun saveRecent(items: List<String>) {
        prefs.edit().putString("recent", items.take(8).joinToString("\n")).apply()
    }

    private fun remember(target: String) {
        val items = recentItems().toMutableList()
        items.remove(target)
        items.add(0, target)
        saveRecent(items)
        refreshRecent()
    }

    private fun refreshRecent() {
        val adapter = ArrayAdapter(
            this, android.R.layout.simple_list_item_1, recentItems()
        )
        recentView.adapter = adapter
    }

    private fun openViewer(raw: String) {
        if (raw.isBlank()) {
            toast("Enter an address or RDP Link ID")
            return
        }
        var target = raw
        if (!target.startsWith("http")) {
            val digits = target.replace(" ", "")
            if (digits.length == 9 && digits.all { it.isDigit() }) {
                resolvePeer(digits)
                return
            }
            target = "http://$target"
        }
        remember(target)
        startActivity(
            Intent(this, ViewerActivity::class.java).putExtra(ViewerActivity.EXTRA_URL, target)
        )
    }

    /** Resolve a 9-digit ID through the configured rendezvous/relay. */
    private fun resolvePeer(digits: String) {
        val base = rendezvousView.text.toString().trim().trimEnd('/')
        if (base.isEmpty()) {
            toast("Set a rendezvous URL in Advanced settings")
            return
        }
        toast("Resolving $digits…")
        thread {
            val resolved = try {
                val url = URL("$base/resolve?peerId=$digits")
                val conn = url.openConnection() as HttpURLConnection
                conn.connectTimeout = 6000
                conn.readTimeout = 6000
                val body = conn.inputStream.bufferedReader().use { it.readText() }
                val json = JSONObject(body)
                json.optString("url").ifEmpty { json.optString("addr") }
            } catch (_: Exception) {
                ""
            }
            runOnUiThread {
                if (resolved.isEmpty()) {
                    toast("Could not resolve $digits")
                } else {
                    var target = resolved
                    if (!target.startsWith("http")) target = "http://$target"
                    remember(target)
                    startActivity(
                        Intent(this, ViewerActivity::class.java)
                            .putExtra(ViewerActivity.EXTRA_URL, target)
                    )
                }
            }
        }
    }

    override fun onPause() {
        ui.removeCallbacks(statusTick)
        saveSettings()
        super.onPause()
    }

    override fun onResume() {
        super.onResume()
        refreshRemoteControlStatus()
        // The accessibility service can bind a moment after the activity
        // resumes; keep the tag in sync while the screen is visible.
        ui.removeCallbacks(statusTick)
        ui.postDelayed(statusTick, 800)
    }

    /** Ant Design "Tag" styling for the status line. */
    private fun setStatus(text: String, kind: String = "neutral") {
        statusView.text = text
        val (background, foreground) = when (kind) {
            "success" -> R.drawable.bg_tag_success to R.color.antd_success_text
            "error" -> R.drawable.bg_tag_error to R.color.antd_error_text
            else -> R.drawable.bg_tag_neutral to R.color.antd_text_secondary
        }
        statusView.setBackgroundResource(background)
        statusView.setTextColor(resources.getColor(foreground, theme))
    }

    private fun refreshRemoteControlStatus() {
        if (sharing) return  // keep the sharing message visible
        val a11y = InputAccessibilityService.instance != null || accessibilityServiceEnabled()
        if (a11y) {
            setStatus("Remote control enabled", "success")
        } else {
            setStatus("Remote control disabled — taps will not be injected", "error")
        }
    }

    private fun accessibilityServiceEnabled(): Boolean {
        val manager = getSystemService(AccessibilityManager::class.java)
        val viaManager = manager
            ?.getEnabledAccessibilityServiceList(AccessibilityServiceInfo.FEEDBACK_ALL_MASK)
            ?.any { it.resolveInfo.serviceInfo.packageName == packageName }
            ?: false
        if (viaManager) return true
        // Fall back to the secure setting, which is readable without a
        // permission and survives before the manager refreshes its cache.
        val enabled = Settings.Secure.getString(
            contentResolver, Settings.Secure.ENABLED_ACCESSIBILITY_SERVICES
        ) ?: return false
        val component = "$packageName/${InputAccessibilityService::class.java.name}"
        return enabled.split(':').any { it.equals(component, ignoreCase = true) }
    }

    private fun saveSettings() {
        val port = portView.text.toString().toIntOrNull() ?: HostService.DEFAULT_PORT
        prefs.edit()
            .putString("password", passwordView.text.toString())
            .putInt("port", port)
            .putString("rendezvous", rendezvousView.text.toString().trim())
            .apply()
    }

    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode == REQUEST_CODE && resultCode == RESULT_OK && data != null) {
            val intent = Intent(this, HostService::class.java)
            intent.putExtra("resultCode", resultCode)
            intent.putExtra("data", data)
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                startForegroundService(intent)
            } else {
                startService(intent)
            }
            sharing = true
            setStatus("Sharing started on port ${portView.text}", "success")
        } else if (requestCode == REQUEST_CODE) {
            setStatus("Screen capture permission denied", "error")
        }
    }

    private fun requestNotificationPermission() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
            checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), NOTIFICATION_REQUEST)
        }
    }

    private fun randomPassword(): String {
        val r = SecureRandom()
        return (100000 + r.nextInt(900000)).toString()
    }

    private fun toast(message: String) {
        Toast.makeText(this, message, Toast.LENGTH_SHORT).show()
    }
}
