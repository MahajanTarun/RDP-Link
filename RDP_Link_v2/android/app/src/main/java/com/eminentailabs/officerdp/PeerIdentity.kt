package com.eminentailabs.officerdp

import android.content.Context
import java.security.MessageDigest
import java.util.UUID

/**
 * Stable 9-digit peer id in the same format as the Go core ("123 456 789").
 *
 * The Go core is bound through gomobile, so the id is derived from the same
 * Ed25519 identity file the desktop agent uses. If the native binding is not
 * available (for example a stripped build), a SHA-256 fallback keeps the app
 * usable with a stable local id.
 */
object PeerIdentity {
    private const val PREFS = "officerdp"
    private const val KEY_ID = "peerId"
    private const val KEY_SEED = "peerSeed"

    fun get(context: Context): String {
        val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        prefs.getString(KEY_ID, null)?.let { return it }

        val id = try {
            mobile.Mobile.peerID(context.filesDir.absolutePath)
        } catch (_: Throwable) {
            val seed = prefs.getString(KEY_SEED, null) ?: UUID.randomUUID().toString().also {
                prefs.edit().putString(KEY_SEED, it).apply()
            }
            format(seed)
        }
        prefs.edit().putString(KEY_ID, id).apply()
        return id
    }

    fun format(seed: String): String {
        val digest = MessageDigest.getInstance("SHA-256").digest(seed.toByteArray())
        var value = 0L
        for (i in 0 until 8) {
            value = (value shl 8) or (digest[i].toLong() and 0xff)
        }
        val digits = (Math.abs(value) % 1_000_000_000L).toString().padStart(9, '0')
        return "${digits.substring(0, 3)} ${digits.substring(3, 6)} ${digits.substring(6, 9)}"
    }
}
