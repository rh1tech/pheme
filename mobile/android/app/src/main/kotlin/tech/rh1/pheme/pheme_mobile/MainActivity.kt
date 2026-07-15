package tech.rh1.pheme.pheme_mobile

import android.content.Context
import android.os.PowerManager
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
