package tech.rh1.pheme.pheme_mobile

import android.content.Context
import android.content.Intent
import android.graphics.BitmapFactory
import android.os.PowerManager
import androidx.core.app.Person
import androidx.core.content.pm.ShortcutInfoCompat
import androidx.core.content.pm.ShortcutManagerCompat
import androidx.core.graphics.drawable.IconCompat
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel

/**
 * Hosts the proximity screen-off wake lock a voice call needs.
 *
 * When the phone is held to the ear the screen must go dark — so a cheek does not tap the call
 * controls, and to save power. That is a PROXIMITY_SCREEN_OFF_WAKE_LOCK, and there is no Flutter API
 * for it, so the call engine drives it over a method channel: acquire when a call connects, release
 * when it ends. Held on the Activity so it is released if the Activity is destroyed mid-call.
 */
class MainActivity : FlutterActivity() {
    private var proximityLock: PowerManager.WakeLock? = null

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, "pheme/conversations")
            .setMethodCallHandler { call, result ->
                when (call.method) {
                    "publishShortcut" -> {
                        publishConversationShortcut(
                            call.argument<String>("id"),
                            call.argument<String>("name"),
                            call.argument<ByteArray>("avatar"),
                        )
                        result.success(null)
                    }
                    else -> result.notImplemented()
                }
            }

        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, "pheme/proximity")
            .setMethodCallHandler { call, result ->
                when (call.method) {
                    "acquire" -> {
                        acquireProximityLock()
                        result.success(null)
                    }
                    "release" -> {
                        releaseProximityLock()
                        result.success(null)
                    }
                    else -> result.notImplemented()
                }
            }
    }

    /**
     * Publishes a conversation shortcut, which is what lets Android treat a message notification as
     * a CONVERSATION rather than an app alert.
     *
     * MessagingStyle alone is not enough. Without a long-lived dynamic shortcut whose id the
     * notification quotes, the system renders it in the ordinary section: the app icon takes the
     * main slot with "Pheme" above it, and the sender's avatar is relegated to a small circle beside
     * the message. With one, the avatar becomes the notification's own icon and the app icon is
     * badged into its corner — the arrangement every messenger uses, because the notification is
     * about the person, not about us.
     *
     * Published from the Activity, and therefore only while the app is running. That is a real
     * limitation and it is the reason this is called when a conversation list loads rather than when
     * a notification arrives: a push handled by the background isolate cannot reach this channel,
     * so the shortcut has to already exist by then. Shortcuts persist, so it does after the first
     * time the app has seen the conversation.
     */
    private fun publishConversationShortcut(id: String?, name: String?, avatar: ByteArray?) {
        if (id.isNullOrEmpty()) return
        val label = if (name.isNullOrEmpty()) "Chat" else name

        // An adaptive bitmap, so the launcher and the notification both round it the way they round
        // every other avatar. A decode failure is not worth failing over: the shortcut is still
        // useful without a picture, and the notification simply falls back to the app icon.
        val icon = avatar?.let { bytes ->
            BitmapFactory.decodeByteArray(bytes, 0, bytes.size)
                ?.let { IconCompat.createWithAdaptiveBitmap(it) }
        }

        val person = Person.Builder()
            .setName(label)
            .setKey(id)
            .apply { icon?.let { setIcon(it) } }
            .build()

        val intent = Intent(this, MainActivity::class.java)
            .setAction(Intent.ACTION_VIEW)
            .putExtra("conversationId", id)

        val shortcut = ShortcutInfoCompat.Builder(this, id)
            .setShortLabel(label)
            .setLongLabel(label)
            .setPerson(person)
            // Long-lived is required: a shortcut the system may discard is not one a notification
            // can be anchored to.
            .setLongLived(true)
            .setCategories(setOf("android.shortcut.conversation"))
            .setIntent(intent)
            .apply { icon?.let { setIcon(it) } }
            .build()

        runCatching { ShortcutManagerCompat.pushDynamicShortcut(this, shortcut) }
    }

    private fun acquireProximityLock() {
        if (proximityLock?.isHeld == true) return
        val power = getSystemService(Context.POWER_SERVICE) as PowerManager
        if (!power.isWakeLockLevelSupported(PowerManager.PROXIMITY_SCREEN_OFF_WAKE_LOCK)) return
        proximityLock = power
            .newWakeLock(PowerManager.PROXIMITY_SCREEN_OFF_WAKE_LOCK, "pheme:call:proximity")
            .also { it.acquire() }
    }

    private fun releaseProximityLock() {
        proximityLock?.let { if (it.isHeld) it.release() }
        proximityLock = null
    }

    override fun onDestroy() {
        releaseProximityLock()
        super.onDestroy()
    }
}
