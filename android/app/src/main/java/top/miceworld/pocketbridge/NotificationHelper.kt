package top.miceworld.pocketbridge

import android.Manifest
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.content.ContextCompat

object NotificationHelper {
    const val SERVICE_CHANNEL_ID = "pocket_bridge_service"
    const val LEGACY_MESSAGE_CHANNEL_ID = "pocket_bridge_messages"
    const val MESSAGE_CHANNEL_ID = "pocket_bridge_messages_v2"
    const val SERVICE_NOTIFICATION_ID = 1001
    const val ACTION_COPY_NOTIFICATION = "top.miceworld.pocketbridge.action.COPY_NOTIFICATION"
    const val EXTRA_NOTIFICATION_ID = "notification_id"
    const val EXTRA_COPY_TITLE = "copy_title"
    const val EXTRA_COPY_BODY = "copy_body"

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
        val notificationId = (System.currentTimeMillis() % Int.MAX_VALUE).toInt()
        val notification = NotificationCompat.Builder(context, MESSAGE_CHANNEL_ID)
            .setSmallIcon(android.R.drawable.stat_notify_more)
            .setContentTitle(title)
            .setContentText(body)
            .setStyle(NotificationCompat.BigTextStyle().bigText(body))
            .setCategory(NotificationCompat.CATEGORY_MESSAGE)
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setAutoCancel(true)
            .addAction(buildCopyAction(context, notificationId, title, body))
            .build()
        runCatching {
            manager.notify(notificationId, notification)
        }.onFailure { error ->
            BridgeRuntime.appendLog("系统通知投递失败: ${error.message ?: error.javaClass.simpleName}")
        }
    }

    fun buildClipboardText(title: String, body: String): String {
        val normalizedTitle = title.trim()
        val normalizedBody = body.trim()
        return when {
            normalizedTitle.isBlank() -> normalizedBody
            normalizedBody.isBlank() -> normalizedTitle
            else -> "$normalizedTitle\n$normalizedBody"
        }
    }

    private fun buildCopyAction(
        context: Context,
        notificationId: Int,
        title: String,
        body: String,
    ): NotificationCompat.Action {
        val intent = Intent(context, NotificationCopyActivity::class.java)
            .setAction(ACTION_COPY_NOTIFICATION)
            .addFlags(
                Intent.FLAG_ACTIVITY_NEW_TASK or
                    Intent.FLAG_ACTIVITY_EXCLUDE_FROM_RECENTS or
                    Intent.FLAG_ACTIVITY_NO_ANIMATION,
            )
            .putExtra(EXTRA_NOTIFICATION_ID, notificationId)
            .putExtra(EXTRA_COPY_TITLE, title)
            .putExtra(EXTRA_COPY_BODY, body)
        val pendingIntent = PendingIntent.getActivity(
            context,
            notificationId,
            intent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        return NotificationCompat.Action.Builder(
            android.R.drawable.ic_menu_edit,
            context.getString(R.string.action_copy_notification),
            pendingIntent,
        ).build()
    }
}
