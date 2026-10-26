.class final Lcom/projectjuly/chef/OvenUpdatesDialog$Open;
.super Ljava/lang/Object;
.source "OvenUpdatesDialog.java"

# interfaces
.implements Landroid/view/View$OnClickListener;
.implements Landroid/view/View$OnLongClickListener;

# June Local: updates screen. Opens it from a tap or a long-press on a view
# inside a Chef activity.


# direct methods
.method constructor <init>()V
    .locals 0

    invoke-direct {p0}, Ljava/lang/Object;-><init>()V

    return-void
.end method


# virtual methods
.method public onClick(Landroid/view/View;)V
    .locals 1

    invoke-virtual {p1}, Landroid/view/View;->getContext()Landroid/content/Context;

    move-result-object v0

    invoke-static {v0}, Lcom/projectjuly/chef/OvenUpdatesDialog;->show(Landroid/content/Context;)V

    return-void
.end method

.method public onLongClick(Landroid/view/View;)Z
    .locals 1

    invoke-virtual {p1}, Landroid/view/View;->getContext()Landroid/content/Context;

    move-result-object v0

    invoke-static {v0}, Lcom/projectjuly/chef/OvenUpdatesDialog;->show(Landroid/content/Context;)V

    const/4 v0, 0x1

    return v0
.end method
