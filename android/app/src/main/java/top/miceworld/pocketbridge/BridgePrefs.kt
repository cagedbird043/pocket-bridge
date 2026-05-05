package top.miceworld.pocketbridge

import android.content.Context

data class BridgeConfig(
    val relayUrl: String,
    val deviceId: String,
    val privateKeyBase64: String,
    val notifyTarget: String,
)

object BridgePrefs {
    private const val PREFS = "pocket_bridge"
    private const val KEY_RELAY_URL = "relay_url"
    private const val KEY_DEVICE_ID = "device_id"
    private const val KEY_PRIVATE_KEY_BASE64 = "private_key_base64"
    private const val KEY_NOTIFY_TARGET = "notify_target"
    private const val KEY_FCM_TOKEN = "fcm_token"
    private const val DEFAULT_PHONE_PRIVATE_KEY_BASE64 =
        "MC4CAQAwBQYDK2VwBCIEIFYoCd/7uk5L4EUms4OMI9AvnqfjtQ8uFkRRa4QSSMML"

    fun load(context: Context): BridgeConfig {
        val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        return BridgeConfig(
            relayUrl = prefs.getString(KEY_RELAY_URL, "ws://223.109.140.254:18080/ws") ?: "ws://223.109.140.254:18080/ws",
            deviceId = prefs.getString(KEY_DEVICE_ID, "phone") ?: "phone",
            privateKeyBase64 = prefs.getString(KEY_PRIVATE_KEY_BASE64, DEFAULT_PHONE_PRIVATE_KEY_BASE64)
                ?: DEFAULT_PHONE_PRIVATE_KEY_BASE64,
            notifyTarget = prefs.getString(KEY_NOTIFY_TARGET, "laptop") ?: "laptop",
        )
    }

    fun save(context: Context, config: BridgeConfig) {
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
            .edit()
            .putString(KEY_RELAY_URL, config.relayUrl)
            .putString(KEY_DEVICE_ID, config.deviceId)
            .putString(KEY_PRIVATE_KEY_BASE64, config.privateKeyBase64)
            .putString(KEY_NOTIFY_TARGET, config.notifyTarget)
            .apply()
    }

    fun loadFcmToken(context: Context): String {
        return context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
            .getString(KEY_FCM_TOKEN, "")
            .orEmpty()
    }

    fun saveFcmToken(context: Context, token: String) {
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
            .edit()
            .putString(KEY_FCM_TOKEN, token)
            .apply()
    }
}
