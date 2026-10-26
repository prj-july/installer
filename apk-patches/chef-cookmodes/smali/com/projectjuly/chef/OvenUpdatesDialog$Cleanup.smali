.class final Lcom/projectjuly/chef/OvenUpdatesDialog$Cleanup;
.super Ljava/lang/Object;
.source "OvenUpdatesDialog.java"

# interfaces
.implements Landroid/content/DialogInterface$OnDismissListener;

# June Local: updates screen. Frees the WebView when the screen closes.


# instance fields
.field private final web:Landroid/webkit/WebView;


# direct methods
.method constructor <init>(Landroid/webkit/WebView;)V
    .locals 0

    invoke-direct {p0}, Ljava/lang/Object;-><init>()V

    iput-object p1, p0, Lcom/projectjuly/chef/OvenUpdatesDialog$Cleanup;->web:Landroid/webkit/WebView;

    return-void
.end method


# virtual methods
.method public onDismiss(Landroid/content/DialogInterface;)V
    .locals 1

    iget-object v0, p0, Lcom/projectjuly/chef/OvenUpdatesDialog$Cleanup;->web:Landroid/webkit/WebView;

    invoke-virtual {v0}, Landroid/webkit/WebView;->destroy()V

    return-void
.end method
