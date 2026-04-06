package top.miceworld.pocketbridge

import com.google.firebase.messaging.FirebaseMessagingService
import com.google.firebase.messaging.RemoteMessage

class FirebasePushService : FirebaseMessagingService() {
    override fun onNewToken(token: String) {
        super.onNewToken(token)
        BridgePrefs.saveFcmToken(this, token)
        BridgeRuntime.appendLog("FCM token 已刷新")
    }

    override fun onMessageReceived(message: RemoteMessage) {
        super.onMessageReceived(message)
        PushBridge.handleIncomingMessage(
            context = this,
            data = message.data,
            fallbackTitle = message.notification?.title,
            fallbackBody = message.notification?.body,
        )
    }

    override fun onDeletedMessages() {
        super.onDeletedMessages()
        BridgeRuntime.appendLog("FCM 删除了待投递消息")
    }
}
