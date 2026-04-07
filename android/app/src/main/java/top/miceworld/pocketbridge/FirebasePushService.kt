package top.miceworld.pocketbridge

import android.util.Log
import com.google.firebase.messaging.FirebaseMessagingService
import com.google.firebase.messaging.RemoteMessage

class FirebasePushService : FirebaseMessagingService() {
    override fun onNewToken(token: String) {
        super.onNewToken(token)
        runCatching {
            BridgePrefs.saveFcmToken(this, token)
            BridgeRuntime.appendLog("FCM token 已刷新")
        }.onFailure { error ->
            BridgeRuntime.appendLog("处理 FCM token 失败: ${error.message ?: error.javaClass.simpleName}")
            Log.e("PocketBridge", "onNewToken failed", error)
        }
    }

    override fun onMessageReceived(message: RemoteMessage) {
        super.onMessageReceived(message)
        runCatching {
            PushBridge.handleIncomingMessage(
                context = this,
                data = message.data,
                fallbackTitle = message.notification?.title,
                fallbackBody = message.notification?.body,
            )
        }.onFailure { error ->
            BridgeRuntime.appendLog("处理 FCM 消息失败: ${error.message ?: error.javaClass.simpleName}")
            Log.e("PocketBridge", "onMessageReceived failed", error)
        }
    }

    override fun onDeletedMessages() {
        super.onDeletedMessages()
        runCatching {
            BridgeRuntime.appendLog("FCM 删除了待投递消息")
        }.onFailure { error ->
            Log.e("PocketBridge", "onDeletedMessages failed", error)
        }
    }
}
