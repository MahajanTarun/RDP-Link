package com.eminentailabs.officerdp

import android.app.Activity
import android.content.Intent
import android.os.Build
import android.os.Bundle
import android.view.View
import android.view.WindowInsets
import android.view.WindowInsetsController
import android.Manifest
import android.net.Uri
import android.content.pm.PackageManager
import android.webkit.PermissionRequest
import android.webkit.WebChromeClient
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.FrameLayout

/**
 * Full-screen guest viewer. Loads the host's browser viewer, which handles
 * password auth, frame streaming, pointer and keyboard input.
 */
class ViewerActivity : Activity() {
    companion object {
        const val EXTRA_URL = "url"
    }

    private lateinit var web: WebView
    private var pendingRequest: PermissionRequest? = null
    private var allowedOrigin: Uri? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val url = intent.getStringExtra(EXTRA_URL)?.trim().orEmpty()
        if (url.isEmpty()) {
            finish()
            return
        }
        val target = if (url.startsWith("http")) url else "http://$url"
        allowedOrigin = Uri.parse(target)

        val root = FrameLayout(this)
        web = WebView(this)
        root.addView(
            web,
            FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.MATCH_PARENT,
                FrameLayout.LayoutParams.MATCH_PARENT
            )
        )
        setContentView(root)

        web.settings.apply {
            javaScriptEnabled = true
            domStorageEnabled = true
            cacheMode = WebSettings.LOAD_NO_CACHE
            mediaPlaybackRequiresUserGesture = false
            useWideViewPort = true
            loadWithOverviewMode = true
            builtInZoomControls = false
            displayZoomControls = false
        }
        web.webViewClient = object : WebViewClient() {
            override fun shouldOverrideUrlLoading(
                view: WebView?,
                request: android.webkit.WebResourceRequest?
            ): Boolean {
                val destination = request?.url ?: return true
                // The guest WebView contains a host password/session. Keep
                // navigation inside the host origin; external links must be
                // opened by the user's normal browser, not this privileged
                // WebView.
                if (!sameOrigin(destination)) {
                    android.widget.Toast.makeText(
                        this@ViewerActivity,
                        "External links are blocked in the remote viewer",
                        android.widget.Toast.LENGTH_SHORT
                    ).show()
                    return true
                }
                return false
            }
        }
        web.webChromeClient = object : WebChromeClient() {
            override fun onPermissionRequest(request: PermissionRequest) {
                if (!sameOrigin(request.origin)) {
                    request.deny()
                    return
                }
                val wanted = request.resources.filter {
                    it == PermissionRequest.RESOURCE_AUDIO_CAPTURE ||
                        it == PermissionRequest.RESOURCE_VIDEO_CAPTURE
                }
                if (wanted.isEmpty()) {
                    request.deny()
                    return
                }
                val missing = mutableListOf<String>()
                if (wanted.contains(PermissionRequest.RESOURCE_AUDIO_CAPTURE) &&
                    checkSelfPermission(Manifest.permission.RECORD_AUDIO) !=
                        PackageManager.PERMISSION_GRANTED
                ) missing += Manifest.permission.RECORD_AUDIO
                if (wanted.contains(PermissionRequest.RESOURCE_VIDEO_CAPTURE) &&
                    checkSelfPermission(Manifest.permission.CAMERA) !=
                        PackageManager.PERMISSION_GRANTED
                ) missing += Manifest.permission.CAMERA
                if (missing.isEmpty()) {
                    request.grant(wanted.toTypedArray())
                } else {
                    pendingRequest = request
                    requestPermissions(missing.toTypedArray(), 42)
                }
            }
        }
        web.loadUrl(target)

        enterImmersive()
    }

    private fun sameOrigin(candidate: Uri): Boolean {
        val expected = allowedOrigin ?: return false
        return candidate.scheme.equals(expected.scheme, ignoreCase = true) &&
            candidate.host.equals(expected.host, ignoreCase = true) &&
            effectivePort(candidate) == effectivePort(expected)
    }

    private fun effectivePort(uri: Uri): Int = when {
        uri.port >= 0 -> uri.port
        uri.scheme.equals("https", ignoreCase = true) -> 443
        uri.scheme.equals("http", ignoreCase = true) -> 80
        else -> -1
    }

    private fun enterImmersive() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            window.setDecorFitsSystemWindows(false)
            window.insetsController?.apply {
                hide(WindowInsets.Type.systemBars())
                systemBarsBehavior =
                    WindowInsetsController.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
            }
        } else {
            @Suppress("DEPRECATION")
            window.decorView.systemUiVisibility = (
                View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY
                    or View.SYSTEM_UI_FLAG_HIDE_NAVIGATION
                    or View.SYSTEM_UI_FLAG_FULLSCREEN
                    or View.SYSTEM_UI_FLAG_LAYOUT_STABLE
                )
        }
    }

    override fun onWindowFocusChanged(hasFocus: Boolean) {
        super.onWindowFocusChanged(hasFocus)
        if (hasFocus) enterImmersive()
    }

    override fun onRequestPermissionsResult(
        requestCode: Int,
        permissions: Array<out String>,
        grantResults: IntArray
    ) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        if (requestCode != 42) return
        val granted = grantResults.isNotEmpty() &&
            grantResults.all { it == PackageManager.PERMISSION_GRANTED }
        val request = pendingRequest
        pendingRequest = null
        if (request == null) return
        if (granted) {
            val wanted = request.resources.filter {
                it == PermissionRequest.RESOURCE_AUDIO_CAPTURE ||
                    it == PermissionRequest.RESOURCE_VIDEO_CAPTURE
            }
            if (wanted.isEmpty()) request.deny() else request.grant(wanted.toTypedArray())
        } else {
            request.deny()
        }
    }

    override fun onBackPressed() {
        if (web.canGoBack()) web.goBack() else super.onBackPressed()
    }

    override fun onDestroy() {
        web.loadUrl("about:blank")
        web.destroy()
        super.onDestroy()
    }
}
