package com.eminentailabs.officerdp

import android.app.Activity
import android.content.Intent
import android.os.Build
import android.os.Bundle
import android.view.View
import android.view.WindowInsets
import android.view.WindowInsetsController
import android.Manifest
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
    private var pendingAudio: PermissionRequest? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val url = intent.getStringExtra(EXTRA_URL)?.trim().orEmpty()
        if (url.isEmpty()) {
            finish()
            return
        }
        val target = if (url.startsWith("http")) url else "http://$url"

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
        web.webViewClient = WebViewClient()
        web.webChromeClient = object : WebChromeClient() {
            override fun onPermissionRequest(request: PermissionRequest) {
                val wantsAudio = request.resources.contains(
                    PermissionRequest.RESOURCE_AUDIO_CAPTURE
                )
                when {
                    !wantsAudio -> request.deny()
                    checkSelfPermission(Manifest.permission.RECORD_AUDIO) ==
                        PackageManager.PERMISSION_GRANTED ->
                        request.grant(arrayOf(PermissionRequest.RESOURCE_AUDIO_CAPTURE))
                    else -> {
                        pendingAudio = request
                        requestPermissions(arrayOf(Manifest.permission.RECORD_AUDIO), 42)
                    }
                }
            }
        }
        web.loadUrl(target)

        enterImmersive()
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
        val granted = grantResults.firstOrNull() == PackageManager.PERMISSION_GRANTED
        val request = pendingAudio
        pendingAudio = null
        if (request == null) return
        if (granted) request.grant(arrayOf(PermissionRequest.RESOURCE_AUDIO_CAPTURE))
        else request.deny()
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
