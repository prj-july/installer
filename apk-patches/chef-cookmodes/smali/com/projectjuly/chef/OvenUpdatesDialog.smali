.class public final Lcom/projectjuly/chef/OvenUpdatesDialog;
.super Ljava/lang/Object;
.source "OvenUpdatesDialog.java"

# June Local: updates screen. Shows june-local's own Updates page
# (http://127.0.0.1/local/oven) full screen in a WebView. A Dialog needs no
# manifest entry, which matters because Chef is rebuilt with `apktool d -r`
# and its manifest stays binary. Any failure is logged, never thrown, so this
# can't take Chef down.


# direct methods
.method private constructor <init>()V
    .locals 0

    invoke-direct {p0}, Ljava/lang/Object;-><init>()V

    return-void
.end method

.method public static attach(Landroid/view/View;)V
    .locals 1

    # Long-press on the given view opens the screen.
    if-eqz p0, :done

    new-instance v0, Lcom/projectjuly/chef/OvenUpdatesDialog$Open;

    invoke-direct {v0}, Lcom/projectjuly/chef/OvenUpdatesDialog$Open;-><init>()V

    invoke-virtual {p0, v0}, Landroid/view/View;->setOnLongClickListener(Landroid/view/View$OnLongClickListener;)V

    :done
    return-void
.end method

.method public static show(Landroid/content/Context;)V
    .locals 4

    :try_start_0
    new-instance v0, Landroid/app/Dialog;

    # android.R.style.Theme_Black_NoTitleBar_Fullscreen
    const v1, 0x103000a

    invoke-direct {v0, p0, v1}, Landroid/app/Dialog;-><init>(Landroid/content/Context;I)V

    new-instance v1, Landroid/webkit/WebView;

    invoke-direct {v1, p0}, Landroid/webkit/WebView;-><init>(Landroid/content/Context;)V

    invoke-virtual {v1}, Landroid/webkit/WebView;->getSettings()Landroid/webkit/WebSettings;

    move-result-object v2

    const/4 v3, 0x1

    invoke-virtual {v2, v3}, Landroid/webkit/WebSettings;->setJavaScriptEnabled(Z)V

    invoke-virtual {v2, v3}, Landroid/webkit/WebSettings;->setDomStorageEnabled(Z)V

    # WebSettings.LOAD_NO_CACHE
    const/4 v3, 0x2

    invoke-virtual {v2, v3}, Landroid/webkit/WebSettings;->setCacheMode(I)V

    const/high16 v3, -0x1000000

    invoke-virtual {v1, v3}, Landroid/webkit/WebView;->setBackgroundColor(I)V

    new-instance v2, Lcom/projectjuly/chef/OvenUpdatesDialog$LocalOnly;

    invoke-direct {v2}, Lcom/projectjuly/chef/OvenUpdatesDialog$LocalOnly;-><init>()V

    invoke-virtual {v1, v2}, Landroid/webkit/WebView;->setWebViewClient(Landroid/webkit/WebViewClient;)V

    invoke-virtual {v0, v1}, Landroid/app/Dialog;->setContentView(Landroid/view/View;)V

    new-instance v2, Lcom/projectjuly/chef/OvenUpdatesDialog$Cleanup;

    invoke-direct {v2, v1}, Lcom/projectjuly/chef/OvenUpdatesDialog$Cleanup;-><init>(Landroid/webkit/WebView;)V

    invoke-virtual {v0, v2}, Landroid/app/Dialog;->setOnDismissListener(Landroid/content/DialogInterface$OnDismissListener;)V

    const-string v2, "http://127.0.0.1/local/oven"

    invoke-virtual {v1, v2}, Landroid/webkit/WebView;->loadUrl(Ljava/lang/String;)V

    invoke-virtual {v0}, Landroid/app/Dialog;->show()V
    :try_end_0
    .catch Ljava/lang/Throwable; {:try_start_0 .. :try_end_0} :catch_0

    return-void

    :catch_0
    move-exception v0

    const-string v1, "ProjectJuly"

    const-string v2, "updates screen failed"

    invoke-static {v1, v2, v0}, Landroid/util/Log;->w(Ljava/lang/String;Ljava/lang/String;Ljava/lang/Throwable;)I

    return-void
.end method
