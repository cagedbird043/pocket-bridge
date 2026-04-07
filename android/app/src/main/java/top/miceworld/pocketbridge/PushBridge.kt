package top.miceworld.pocketbridge

import android.content.Context
import com.google.firebase.FirebaseApp
import com.google.firebase.messaging.FirebaseMessaging

object PushBridge {
    const val PROVIDER_FCM = "fcm"

    private const val DATA_KIND = "pb_kind"
    private const val DATA_FROM_DEVICE_ID = "pb_from_device_id"
    private const val DATA_TITLE = "pb_title"
    private const val DATA_BODY = "pb_body"

    fun fetchAndStoreToken(context: Context, onToken: ((String) -> Unit)? = null) {
        if (!isFirebaseConfigured(context)) {
            return
        }

        try {
            FirebaseMessaging.getInstance().token.addOnCompleteListener { task ->
                if (!task.isSuccessful) {
                    BridgeRuntime.appendLog("获取 FCM token 失败: ${task.exception?.message ?: "unknown"}")
                    return@addOnCompleteListener
                }

                val token = task.result?.trim().orEmpty()
                if (token.isBlank()) {
                    return@addOnCompleteListener
                }

                BridgePrefs.saveFcmToken(context, token)
                onToken?.invoke(token)
            }
        } catch (e: Exception) {
            BridgeRuntime.appendLog("FCM 初始化失败: ${e.message ?: e.javaClass.simpleName}")
        }
    }

    fun handleIncomingMessage(
        context: Context,
        data: Map<String, String>,
        fallbackTitle: String? = null,
        fallbackBody: String? = null,
    ) {
        val title = data[DATA_TITLE]?.ifBlank { null } ?: fallbackTitle
        if (title.isNullOrBlank()) {
            BridgeRuntime.appendLog("收到 FCM，但缺少 title")
            return
        }

        val body = data[DATA_BODY] ?: fallbackBody.orEmpty()
        val kind = data[DATA_KIND].orEmpty()
        val fromDeviceId = data[DATA_FROM_DEVICE_ID].orEmpty()

        BridgeRuntime.appendLog(
            buildString {
                append("收到 FCM")
                if (kind.isNotBlank()) {
                    append(" kind=")
                    append(kind)
                }
                if (fromDeviceId.isNotBlank()) {
                    append(" from=")
                    append(fromDeviceId)
                }
                append(" title=")
                append(title)
            },
        )
        runCatching {
            NotificationHelper.ensureChannels(context)
            NotificationHelper.showIncomingNotification(context, title, body)
        }.onFailure { error ->
            BridgeRuntime.appendLog("展示 FCM 通知失败: ${error.message ?: error.javaClass.simpleName}")
        }
    }

    private fun isFirebaseConfigured(context: Context): Boolean {
        return try {
            FirebaseApp.getApps(context).isNotEmpty() || FirebaseApp.initializeApp(context) != null
        } catch (_: Exception) {
            false
        }
    }
}
