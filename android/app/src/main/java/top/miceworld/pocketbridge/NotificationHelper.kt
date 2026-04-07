package top.miceworld.pocketbridge

import android.Manifest
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.content.ContextCompat

object NotificationHelper {
    const val SERVICE_CHANNEL_ID = "pocket_bridge_service"
    const val LEGACY_MESSAGE_CHANNEL_ID = "pocket_bridge_messages"
    const val MESSAGE_CHANNEL_ID = "pocket_bridge_messages_v2"
    const val SERVICE_NOTIFICATION_ID = 1001

    fun ensureChannels(context: Context) {
        val manager = context.getSystemService(NotificationManager::class.java) ?: return
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val serviceChannel = NotificationChannel(
                SERVICE_CHANNEL_ID,
                context.getString(R.string.service_channel_name),
                NotificationManager.IMPORTANCE_LOW,
            ).apply {
                description = context.getString(R.string.service_channel_description)
            }
            val messageChannel = NotificationChannel(
                MESSAGE_CHANNEL_ID,
                context.getString(R.string.message_channel_name),
                NotificationManager.IMPORTANCE_HIGH,
            ).apply {
                description = context.getString(R.string.message_channel_description)
                enableVibration(true)
                setShowBadge(true)
            }
            manager.createNotificationChannel(serviceChannel)
            manager.createNotificationChannel(messageChannel)
        }
    }

    fun buildServiceNotification(context: Context, connected: Boolean): Notification {
        return NotificationCompat.Builder(context, SERVICE_CHANNEL_ID)
            .setSmallIcon(android.R.drawable.stat_notify_sync)
            .setContentTitle(context.getString(R.string.service_notification_title))
            .setContentText(
                if (connected) {
                    context.getString(R.string.service_notification_text_connected)
                } else {
                    context.getString(R.string.service_notification_text_disconnected)
                },
            )
            .setOngoing(true)
            .build()
    }

    fun showIncomingNotification(context: Context, title: String, body: String) {
        if (Build.VERSION.SDK_INT >= 33 &&
            ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            BridgeRuntime.appendLog("通知权限未授予，跳过系统通知：$title")
            return
        }

        val manager = context.getSystemService(NotificationManager::class.java) ?: run {
            BridgeRuntime.appendLog("通知服务不可用，跳过系统通知：$title")
            return
        }
        val notification = NotificationCompat.Builder(context, MESSAGE_CHANNEL_ID)
            .setSmallIcon(android.R.drawable.stat_notify_more)
            .setContentTitle(title)
            .setContentText(body)
            .setStyle(NotificationCompat.BigTextStyle().bigText(body))
            .setCategory(NotificationCompat.CATEGORY_MESSAGE)
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setAutoCancel(true)
            .build()
        runCatching {
            manager.notify((System.currentTimeMillis() % Int.MAX_VALUE).toInt(), notification)
        }.onFailure { error ->
            BridgeRuntime.appendLog("系统通知投递失败: ${error.message ?: error.javaClass.simpleName}")
        }
    }
}
