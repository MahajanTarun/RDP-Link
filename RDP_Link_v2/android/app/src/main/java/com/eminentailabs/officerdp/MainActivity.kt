package com.eminentailabs.officerdp

import android.Manifest
import android.app.Activity
import android.app.AlertDialog
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.view.accessibility.AccessibilityManager
import android.accessibilityservice.AccessibilityServiceInfo
import android.content.pm.PackageManager
import android.graphics.Color
import android.media.projection.MediaProjectionManager
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.provider.Settings
import android.view.View
import android.view.WindowInsets
import android.view.WindowInsetsController
import android.widget.ArrayAdapter
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ListView
import android.widget.ScrollView
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
    private lateinit var headerView: View
    private lateinit var actionBarView: View
    private lateinit var scrollContentView: View
    private var headerBasePadding = IntArray(4)
    private var actionBarBasePadding = IntArray(4)
    private var scrollBasePadding = IntArray(4)

    private val prefs by lazy { getSharedPreferences("officerdp", Context.MODE_PRIVATE) }

    private var sharing = false
    private val ui = Handler(Looper.getMainLooper())
    private val statusTick = object : Runnable {
        override fun run() {
            val running = isHostRunning()
            if (running != sharing) {
                sharing = running
                if (!running) setStatus("Sharing stopped", "neutral")
            }
            if (!sharing) refreshRemoteControlStatus()
            ui.postDelayed(this, 2000)
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)
        configureEdgeToEdge()
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
        headerView = findViewById(R.id.header)
        actionBarView = findViewById(R.id.actionBar)
        scrollContentView = findViewById(R.id.scrollContent)
        installSystemBarInsets()
        statusView.setOnClickListener {
            startActivity(Intent(Settings.ACTION_ACCESSIBILITY_SETTINGS))
            toast("Enable RDP Link remote control")
        }

        peerIdView.text = PeerIdentity.get(this)
        passwordView.setText(prefs.getString("password", "") ?: "")
        portView.setText(prefs.getInt("port", HostService.DEFAULT_PORT).toString())
        rendezvousView.setText(prefs.getString("rendezvous", "") ?: "")
        urlView.setText(savedInstanceState?.getString("url") ?: "")

        // The activity is recreated on rotation so the right layout variant
        // (portrait / landscape / tablet) is selected. Restore the panel
        // state that is not already kept in preferences.
        if (savedInstanceState?.getBoolean("advancedOpen", false) == true) {
            advancedPanel.visibility = View.VISIBLE
            advancedToggle.text = "Advanced settings ▴"
        }

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
            prefs.edit().putBoolean("sharing", false).remove("host_heartbeat").apply()
            stopService(Intent(this, HostService::class.java))
            setStatus("Sharing stopped")
            refreshRemoteControlStatus()
        }
        findViewById<Button>(R.id.enableA11y).setOnClickListener {
            startActivity(Intent(Settings.ACTION_ACCESSIBILITY_SETTINGS))
            toast("Enable RDP Link remote control")
        }
        findViewById<Button>(R.id.setupBtn).setOnClickListener { showSetup() }

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

        // -- bottom navigation / tabs ---------------------------------------
        val tabHome = findViewById<View>(R.id.tabHome)
        val tabDevices = findViewById<View>(R.id.tabDevices)
        val tabFiles = findViewById<View>(R.id.tabFiles)
        val tabSettings = findViewById<View>(R.id.tabSettings)
        val navHome = findViewById<TextView>(R.id.navHome)
        val navDevices = findViewById<TextView>(R.id.navDevices)
        val navFiles = findViewById<TextView>(R.id.navFiles)
        val navSettings = findViewById<TextView>(R.id.navSettings)

        fun showTab(name: String) {
            tabHome.visibility = if (name == "home") View.VISIBLE else View.GONE
            tabDevices.visibility = if (name == "devices") View.VISIBLE else View.GONE
            tabFiles.visibility = if (name == "files") View.VISIBLE else View.GONE
            tabSettings.visibility = if (name == "settings") View.VISIBLE else View.GONE
            val activeColor = resources.getColor(R.color.antd_primary, theme)
            val inactiveColor = resources.getColor(R.color.antd_text_secondary, theme)
            navHome.setTextColor(if (name == "home") activeColor else inactiveColor)
            navDevices.setTextColor(if (name == "devices") activeColor else inactiveColor)
            navFiles.setTextColor(if (name == "files") activeColor else inactiveColor)
            navSettings.setTextColor(if (name == "settings") activeColor else inactiveColor)
        }
        navHome.setOnClickListener { showTab("home") }
        navDevices.setOnClickListener { showTab("devices") }
        navFiles.setOnClickListener { showTab("files") }
        navSettings.setOnClickListener { showTab("settings") }

        findViewById<Button>(R.id.devA11yBtn).setOnClickListener {
            startActivity(Intent(Settings.ACTION_ACCESSIBILITY_SETTINGS))
            toast("Enable RDP Link remote control")
        }
        findViewById<Button>(R.id.devSetupBtn).setOnClickListener { showSetup() }

        findViewById<Button>(R.id.filesOpenViewerBtn).setOnClickListener {
            val raw = urlView.text.toString().trim()
            if (raw.isNotBlank()) openViewer(raw) else toast("Enter an address first")
        }

        val settingsPort = findViewById<EditText>(R.id.settingsPort)
        val settingsRendezvous = findViewById<EditText>(R.id.settingsRendezvous)
        settingsPort.setText(portView.text)
        settingsRendezvous.setText(rendezvousView.text)
        findViewById<Button>(R.id.darkModeToggle).setOnClickListener {
            toast("Dark mode preference saved")
        }
        findViewById<Button>(R.id.settingsSaveBtn).setOnClickListener {
            portView.setText(settingsPort.text)
            rendezvousView.setText(settingsRendezvous.text)
            saveSettings()
            toast("Settings saved")
        }

        showTab("home")

        if (savedInstanceState == null) {
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
        }

        sharing = isHostRunning()
        if (sharing) {
            setStatus("Sharing started on port ${portView.text}", "success")
        }

        if (prefs.getString("password", "").isNullOrEmpty()) {
            passwordView.setText(randomPassword())
            saveSettings()
        }
    }

    // -- setup ---------------------------------------------------------------
    private fun showSetup() {
        val density = resources.displayMetrics.density
        fun dp(v: Int) = (v * density).toInt()
        val dialog = AlertDialog.Builder(this)
            .setTitle("Setup & permissions")
            .setNegativeButton("Close", null)
            .create()

        val scroll = ScrollView(this)
        val col = LinearLayout(this)
        col.orientation = LinearLayout.VERTICAL
        col.setPadding(dp(20), dp(12), dp(20), dp(12))
        scroll.addView(col)

        fun addRow(title: String, detail: String, ok: Boolean, action: String,
                   onClick: (() -> Unit)?) {
            val card = LinearLayout(this)
            card.orientation = LinearLayout.VERTICAL
            card.setPadding(dp(4), dp(12), dp(4), dp(12))
            val head = LinearLayout(this)
            head.orientation = LinearLayout.HORIZONTAL
            head.gravity = android.view.Gravity.CENTER_VERTICAL
            val name = TextView(this)
            name.text = title
            name.textSize = 15f
            name.setTextColor(resources.getColor(R.color.antd_text, theme))
            name.setTypeface(null, android.graphics.Typeface.BOLD)
            name.layoutParams = LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f)
            val tag = TextView(this)
            tag.text = if (ok) "Ready" else "Action needed"
            tag.textSize = 12f
            tag.setTextColor(resources.getColor(
                if (ok) R.color.antd_success_text else R.color.antd_warning_text, theme))
            tag.setBackgroundResource(
                if (ok) R.drawable.bg_tag_success else R.drawable.bg_tag_neutral)
            tag.setPadding(dp(8), dp(2), dp(8), dp(2))
            head.addView(name)
            head.addView(tag)
            card.addView(head)
            val sub = TextView(this)
            sub.text = detail
            sub.textSize = 12f
            sub.setTextColor(resources.getColor(R.color.antd_text_secondary, theme))
            sub.setPadding(0, dp(4), 0, 0)
            card.addView(sub)
            if (onClick != null) {
                val button = Button(this, null, 0, R.style.OfficeButton)
                button.text = action
                button.layoutParams = LinearLayout.LayoutParams(
                    LinearLayout.LayoutParams.WRAP_CONTENT,
                    LinearLayout.LayoutParams.WRAP_CONTENT)
                button.setPadding(dp(14), dp(4), dp(14), dp(4))
                val wrap = LinearLayout(this)
                wrap.setPadding(0, dp(8), 0, 0)
                wrap.addView(button)
                button.setOnClickListener { dialog.dismiss(); onClick() }
                card.addView(wrap)
            }
            val divider = View(this)
            divider.layoutParams = LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, dp(1))
            divider.setBackgroundColor(resources.getColor(R.color.antd_border_secondary, theme))
            col.addView(card)
            col.addView(divider)
        }

        val a11y = InputAccessibilityService.instance != null || accessibilityServiceEnabled()
        addRow("Remote control (accessibility)",
            if (a11y) "Enabled — taps and keys can be injected"
            else "Needed to control this device from a guest",
            a11y, "Open settings") {
            startActivity(Intent(Settings.ACTION_ACCESSIBILITY_SETTINGS))
        }

        val notif = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) ==
                PackageManager.PERMISSION_GRANTED
        } else true
        addRow("Notifications",
            if (notif) "Allowed — sharing status stays visible"
            else "The foreground service needs notification permission",
            notif, "Allow") {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS),
                    NOTIFICATION_REQUEST)
            }
        }

        val pm = getSystemService(POWER_SERVICE) as android.os.PowerManager
        val battery = pm.isIgnoringBatteryOptimizations(packageName)
        addRow("Battery optimisation",
            if (battery) "Exempt — long sessions keep running"
            else "Exempt RDP Link to avoid the OS pausing the host",
            battery, "Open settings") {
            startActivity(Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS))
        }

        addRow("Screen capture",
            "Android asks for approval every time you start sharing. This is an OS security rule and cannot be pre-granted.",
            true, "", null)

        dialog.setView(scroll)
        dialog.show()
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

    /**
     * Draw edge-to-edge, then apply the status/navigation/cutout insets to the
     * header and action bar. This keeps the bottom buttons above the 3-button
     * navigation bar and above the keyboard on every Android version.
     */
    private fun configureEdgeToEdge() {
        window.statusBarColor = Color.TRANSPARENT
        window.navigationBarColor = Color.TRANSPARENT
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            window.setDecorFitsSystemWindows(false)
        }
        window.decorView?.let { decor ->
            @Suppress("DEPRECATION")
            decor.systemUiVisibility = (
                View.SYSTEM_UI_FLAG_LAYOUT_STABLE or
                    View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN or
                    View.SYSTEM_UI_FLAG_LAYOUT_HIDE_NAVIGATION or
                    View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR or
                    View.SYSTEM_UI_FLAG_LIGHT_NAVIGATION_BAR
                )
        }
    }

    private fun installSystemBarInsets() {
        headerBasePadding = intArrayOf(
            headerView.paddingLeft, headerView.paddingTop,
            headerView.paddingRight, headerView.paddingBottom
        )
        actionBarBasePadding = intArrayOf(
            actionBarView.paddingLeft, actionBarView.paddingTop,
            actionBarView.paddingRight, actionBarView.paddingBottom
        )
        scrollBasePadding = intArrayOf(
            scrollContentView.paddingLeft, scrollContentView.paddingTop,
            scrollContentView.paddingRight, scrollContentView.paddingBottom
        )
        val root = findViewById<View>(R.id.root)
        root.setOnApplyWindowInsetsListener { _, insets ->
            val left: Int
            val top: Int
            val right: Int
            val bottom: Int
            val imeBottom: Int
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
                val bars = insets.getInsets(
                    WindowInsets.Type.systemBars() or WindowInsets.Type.displayCutout()
                )
                left = bars.left
                top = bars.top
                right = bars.right
                bottom = bars.bottom
                imeBottom = insets.getInsets(WindowInsets.Type.ime()).bottom
            } else {
                @Suppress("DEPRECATION")
                left = insets.systemWindowInsetLeft
                @Suppress("DEPRECATION")
                top = insets.systemWindowInsetTop
                @Suppress("DEPRECATION")
                right = insets.systemWindowInsetRight
                @Suppress("DEPRECATION")
                bottom = insets.systemWindowInsetBottom
                imeBottom = 0
            }
            headerView.setPadding(
                headerBasePadding[0] + left,
                headerBasePadding[1] + top,
                headerBasePadding[2] + right,
                headerBasePadding[3]
            )
            actionBarView.setPadding(
                actionBarBasePadding[0] + left,
                actionBarBasePadding[1],
                actionBarBasePadding[2] + right,
                actionBarBasePadding[3] + maxOf(bottom, imeBottom)
            )
            scrollContentView.setPadding(
                scrollBasePadding[0] + left,
                scrollBasePadding[1],
                scrollBasePadding[2] + right,
                scrollBasePadding[3]
            )
            insets
        }
        root.requestApplyInsets()
    }

    /** A host service writes a heartbeat every few seconds; a stale value
     *  means the service was killed, so the UI must not pretend it is live. */
    private fun isHostRunning(): Boolean {
        if (!prefs.getBoolean("sharing", false)) return false
        val heartbeat = prefs.getLong("host_heartbeat", 0L)
        return heartbeat > 0L &&
            System.currentTimeMillis() - heartbeat < 20_000L
    }

    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        outState.putString("url", urlView.text.toString())
        outState.putBoolean("advancedOpen", advancedPanel.visibility == View.VISIBLE)
    }

    override fun onPause() {
        ui.removeCallbacks(statusTick)
        saveSettings()
        super.onPause()
    }

    override fun onResume() {
        super.onResume()
        sharing = isHostRunning()
        if (sharing) {
            setStatus("Sharing started on port ${portView.text}", "success")
        } else {
            refreshRemoteControlStatus()
        }
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
        val bound = InputAccessibilityService.instance != null
        val enabled = accessibilityServiceEnabled()
        when {
            bound -> setStatus("Remote control enabled", "success")
            enabled -> setStatus("Remote control enabled — waiting for Android to bind", "neutral")
            else -> setStatus("Remote control disabled — taps will not be injected", "error")
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
            prefs.edit()
                .putBoolean("sharing", true)
                .putLong("host_heartbeat", System.currentTimeMillis())
                .apply()
            setStatus("Sharing started on port ${portView.text}", "success")
        } else if (requestCode == REQUEST_CODE) {
            sharing = false
            prefs.edit().putBoolean("sharing", false).remove("host_heartbeat").apply()
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
