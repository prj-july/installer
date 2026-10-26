.class final Lcom/projectjuly/chef/OvenUpdatesDialog$LocalOnly;
.super Landroid/webkit/WebViewClient;
.source "OvenUpdatesDialog.java"

# June Local: updates screen. Keeps the WebView on june-local; anything else
# is refused rather than opened.


# direct methods
.method constructor <init>()V
    .locals 0

    invoke-direct {p0}, Landroid/webkit/WebViewClient;-><init>()V

    return-void
.end method


# virtual methods
.method public shouldOverrideUrlLoading(Landroid/webkit/WebView;Ljava/lang/String;)Z
    .locals 1

    if-eqz p2, :block

    const-string v0, "http://127.0.0.1/"

    invoke-virtual {p2, v0}, Ljava/lang/String;->startsWith(Ljava/lang/String;)Z

    move-result v0

    if-eqz v0, :block

    const/4 v0, 0x0

    return v0

    :block
    const/4 v0, 0x1

    return v0
.end method
