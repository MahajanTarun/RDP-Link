package com.eminentailabs.officerdp

import android.accessibilityservice.AccessibilityService
import android.accessibilityservice.GestureDescription
import android.content.ClipboardManager
import android.graphics.Path
import android.os.Bundle
import android.view.accessibility.AccessibilityEvent
import android.view.accessibility.AccessibilityNodeInfo

/**
 * Accessibility service used by the host to inject taps and keyboard input
 * from a remote guest. It only performs gestures and text edits; it never
 * reads screen content for transmission.
 */
class InputAccessibilityService : AccessibilityService() {
    companion object {
        @Volatile
        var instance: InputAccessibilityService? = null
            private set
    }

    override fun onServiceConnected() {
        super.onServiceConnected()
        instance = this
    }

    override fun onDestroy() {
        if (instance === this) instance = null
        super.onDestroy()
    }

    /**
     * The clipboard of the host device.
     *
     * From Android 10 on, a background app cannot read the clipboard. An
     * accessibility service is allowed to, so the last value is cached here
     * whenever the user copies something and the host serves that cache.
     */
    @Volatile
    private var cachedClipboard: String = ""

    fun clipboardCache(): String = cachedClipboard

    override fun onAccessibilityEvent(event: AccessibilityEvent?) {
        val type = event?.eventType ?: return
        when (type) {
            AccessibilityEvent.TYPE_TEXT_SELECTION_CHANGED,
            AccessibilityEvent.TYPE_VIEW_TEXT_CHANGED,
            AccessibilityEvent.TYPE_WINDOW_CONTENT_CHANGED -> captureClipboard()
        }
    }

    override fun onInterrupt() {}

    private fun captureClipboard() {
        try {
            val manager = getSystemService(ClipboardManager::class.java) ?: return
            val clip = manager.primaryClip ?: return
            if (clip.itemCount == 0) return
            val text = clip.getItemAt(0).coerceToText(this)?.toString().orEmpty()
            if (text.isNotEmpty()) cachedClipboard = text
        } catch (_: Exception) {
            // The OS refused the read; the cache simply keeps the old value.
        }
    }

    fun tap(x: Float, y: Float) {
        val path = Path().apply { moveTo(x, y) }
        val stroke = GestureDescription.StrokeDescription(path, 0, 50)
        val gesture = GestureDescription.Builder().addStroke(stroke).build()
        dispatchGesture(gesture, null, null)
    }

    fun scroll(direction: Int) {
        val metrics = resources.displayMetrics
        val cx = metrics.widthPixels / 2f
        val startY = metrics.heightPixels * 0.7f
        val endY = if (direction > 0) metrics.heightPixels * 0.3f else metrics.heightPixels * 0.9f
        val path = Path().apply {
            moveTo(cx, startY)
            lineTo(cx, endY)
        }
        val stroke = GestureDescription.StrokeDescription(path, 0, 220)
        val gesture = GestureDescription.Builder().addStroke(stroke).build()
        dispatchGesture(gesture, null, null)
    }

    fun key(name: String) {
        when (name.lowercase()) {
            "enter", "return" -> appendText("\n")
            "backspace", "delete" -> backspace()
            "back", "escape", "esc" -> performGlobalAction(GLOBAL_ACTION_BACK)
            "home" -> performGlobalAction(GLOBAL_ACTION_HOME)
            "recents", "appswitch" -> performGlobalAction(GLOBAL_ACTION_RECENTS)
            "notifications" -> performGlobalAction(GLOBAL_ACTION_NOTIFICATIONS)
            "tab" -> appendText("\t")
            "" -> Unit
            else -> appendText(name)
        }
    }

    fun appendText(text: String) {
        val node = focusedInput() ?: return
        val current = node.text?.toString().orEmpty()
        setText(node, current + text)
    }

    private fun backspace() {
        val node = focusedInput() ?: return
        val current = node.text?.toString().orEmpty()
        if (current.isNotEmpty()) setText(node, current.dropLast(1))
    }

    private fun focusedInput(): AccessibilityNodeInfo? {
        val root = rootInActiveWindow ?: return null
        return root.findFocus(AccessibilityNodeInfo.FOCUS_INPUT)
    }

    private fun setText(node: AccessibilityNodeInfo, text: String) {
        val args = Bundle().apply {
            putCharSequence(AccessibilityNodeInfo.ACTION_ARGUMENT_SET_TEXT_CHARSEQUENCE, text)
        }
        node.performAction(AccessibilityNodeInfo.ACTION_SET_TEXT, args)
    }
}
