package top.miceworld.pocketbridge

import android.content.Context

data class BridgeConfig(
    val relayUrl: String,
    val deviceId: String,
    val privateKeyBase64: String,
)

object BridgePrefs {
    private const val PREFS = "pocket_bridge"
    private const val KEY_RELAY_URL = "relay_url"
    private const val KEY_DEVICE_ID = "device_id"
    private const val KEY_PRIVATE_KEY_BASE64 = "private_key_base64"
    private const val DEFAULT_PHONE_PRIVATE_KEY_BASE64 =
        "MC4CAQAwBQYDK2VwBCIEIFYoCd/7uk5L4EUms4OMI9AvnqfjtQ8uFkRRa4QSSMML"

    fun load(context: Context): BridgeConfig {
        val prefs = context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
        return BridgeConfig(
            relayUrl = prefs.getString(KEY_RELAY_URL, "ws://10.0.2.2:18080/ws") ?: "ws://10.0.2.2:18080/ws",
            deviceId = prefs.getString(KEY_DEVICE_ID, "phone") ?: "phone",
            privateKeyBase64 = prefs.getString(KEY_PRIVATE_KEY_BASE64, DEFAULT_PHONE_PRIVATE_KEY_BASE64)
                ?: DEFAULT_PHONE_PRIVATE_KEY_BASE64,
        )
    }

    fun save(context: Context, config: BridgeConfig) {
        context.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
            .edit()
            .putString(KEY_RELAY_URL, config.relayUrl)
            .putString(KEY_DEVICE_ID, config.deviceId)
            .putString(KEY_PRIVATE_KEY_BASE64, config.privateKeyBase64)
            .apply()
    }
}
