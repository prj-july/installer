#!/usr/bin/env python3
"""Patch the decoded Chef app so every cook primitive can be started remotely.

The stock oven rejects every remote primitive start except bake, roast, airfry,
pizzaiolo, sous_vide and grill (CookParamValidator.isValidFoodToStartRemotely).
It also has no way to say *which* plan of a multi-plan primitive to start, so
remote toast always lands on the first plan (level 1).

This patch:

  * widens CookParamValidator.isValidFoodToStartRemotely to every primitive the
    oven's own primitive map knows, except the blow_dry cleaning cycle;
  * adds an optional ``plan_index`` field (0-based) to the 11002
    StartCookCommandData JSON model;
  * teaches RemoteFoodLoadListener to select that plan before starting.

Wire example (toast level 5 -> plan_index 4):

    11002 {"primitive_type":"toast","temperature_cavity":260000,"plan_index":4}

Omitted or negative plan_index keeps the stock behaviour (first plan), so old
clients are unaffected.

It also adds a "Project July" tile to Chef's own home-screen launcher. The tile
(mode 0x30) opens june-local's oven-side Updates page
(http://127.0.0.1/local/oven) in the full-screen OvenUpdatesDialog WebView. A
home-screen tile, not a Settings row: Chef's Settings screen belongs to the
separate JuneSettings.apk, which a Chef-only patch cannot change.

Usage: python3 patch.py <apktool-decoded-dir>
"""

import os
import sys

MARKER = "June Local: cook-modes"


def read(path):
    with open(path, "r", encoding="utf-8") as handle:
        return handle.read()


def write(path, text):
    with open(path, "w", encoding="utf-8") as handle:
        handle.write(text)


def replace_method(text, method, replacement):
    """Replace one complete smali method, keeping everything around it."""
    start = text.find(method)
    if start < 0:
        raise SystemExit("method not found: %s" % method.splitlines()[0])
    end = text.find(".end method", start)
    if end < 0:
        raise SystemExit("method end not found: %s" % method.splitlines()[0])
    end += len(".end method")
    return text[:start] + replacement + text[end:]


def patch_validator(decoded):
    path = os.path.join(
        decoded,
        "smali/com/junelife/device/chef/oven/CookParamValidator.smali",
    )
    text = read(path)
    if MARKER in text:
        print("[=] CookParamValidator already patched")
        return
    method = """.method public static isValidFoodToStartRemotely(Ljava/lang/String;)Z"""
    replacement = """.method public static isValidFoodToStartRemotely(Ljava/lang/String;)Z
    .locals 1

    # June Local: cook-modes. Allow every primitive in the oven's own
    # primitive map except the blow_dry cleaning cycle. This is the same set
    # of modes the oven screen offers.
    if-eqz p0, :unsupported

    const-string v0, "blow_dry"

    invoke-virtual {v0, p0}, Ljava/lang/String;->equals(Ljava/lang/Object;)Z

    move-result v0

    if-nez v0, :unsupported

    # slow_cook plans carry 0-duration placeholder steps until the UI's
    # duration screen fills them; starting one remotely reboots the oven.
    # Block it until the 11002 duration_ms extension lands.
    const-string v0, "slow_cook"

    invoke-virtual {v0, p0}, Ljava/lang/String;->equals(Ljava/lang/Object;)Z

    move-result v0

    if-nez v0, :unsupported

    sget-object v0, Lcom/junelife/device/chef/ui/cv/CookPrograms;->PRIMITIVE_PROGRAM_MAP:Ljava/util/HashMap;

    invoke-virtual {v0, p0}, Ljava/util/HashMap;->containsKey(Ljava/lang/Object;)Z

    move-result v0

    return v0

    :unsupported
    const/4 v0, 0x0

    return v0
.end method"""
    write(path, replace_method(text, method, replacement))
    print("[+] CookParamValidator.isValidFoodToStartRemotely widened")


def patch_start_cook_command_data(decoded):
    path = os.path.join(
        decoded,
        "smali/com/junelife/device/chef/webservices/model/StartCookCommandData.smali",
    )
    text = read(path)
    if MARKER in text:
        print("[=] StartCookCommandData already patched")
        return

    field = """# June Local: cook-modes
.field private planIndex:I
    .annotation runtime Lcom/fasterxml/jackson/annotation/JsonProperty;
        value = "plan_index"
    .end annotation
.end field

"""
    anchor = "# direct methods"
    text = text.replace(anchor, field + anchor, 1)

    # The no-arg constructor is what Jackson uses; default to -1 (no override).
    no_arg = """    invoke-direct {p0}, Ljava/lang/Object;-><init>()V

    return-void
.end method"""
    no_arg_new = """    invoke-direct {p0}, Ljava/lang/Object;-><init>()V

    const/4 v0, -0x1

    iput v0, p0, Lcom/junelife/device/chef/webservices/model/StartCookCommandData;->planIndex:I

    return-void
.end method"""
    count = text.count(no_arg)
    if count != 1:
        raise SystemExit("StartCookCommandData no-arg constructor anchor matched %d" % count)
    text = text.replace(no_arg, no_arg_new, 1)
    # That constructor needs a local for the constant.
    text = text.replace(
        """.method private constructor <init>()V
    .locals 0""",
        """.method private constructor <init>()V
    .locals 1""",
        1,
    )

    # Parcel round-trip: keep plan_index with the other fields.
    parcel_tail = """    invoke-virtual {p1}, Landroid/os/Parcel;->readString()Ljava/lang/String;

    move-result-object p1

    iput-object p1, p0, Lcom/junelife/device/chef/webservices/model/StartCookCommandData;->recipeId:Ljava/lang/String;

    return-void"""
    parcel_tail_new = """    invoke-virtual {p1}, Landroid/os/Parcel;->readString()Ljava/lang/String;

    move-result-object v0

    iput-object v0, p0, Lcom/junelife/device/chef/webservices/model/StartCookCommandData;->recipeId:Ljava/lang/String;

    invoke-virtual {p1}, Landroid/os/Parcel;->readInt()I

    move-result v0

    iput v0, p0, Lcom/junelife/device/chef/webservices/model/StartCookCommandData;->planIndex:I

    return-void"""
    count = text.count(parcel_tail)
    if count != 1:
        raise SystemExit("StartCookCommandData parcel anchor matched %d" % count)
    text = text.replace(parcel_tail, parcel_tail_new, 1)

    four_arg = """.method public constructor <init>(Ljava/lang/String;ILjava/lang/String;Ljava/lang/String;)V
    .locals 0

    .line 39
    invoke-direct {p0}, Ljava/lang/Object;-><init>()V

    .line 40
    iput-object p1, p0, Lcom/junelife/device/chef/webservices/model/StartCookCommandData;->foodName:Ljava/lang/String;
"""
    four_arg_new = """.method public constructor <init>(Ljava/lang/String;ILjava/lang/String;Ljava/lang/String;)V
    .locals 1

    .line 39
    invoke-direct {p0}, Ljava/lang/Object;-><init>()V

    const/4 v0, -0x1

    iput v0, p0, Lcom/junelife/device/chef/webservices/model/StartCookCommandData;->planIndex:I

    .line 40
    iput-object p1, p0, Lcom/junelife/device/chef/webservices/model/StartCookCommandData;->foodName:Ljava/lang/String;
"""
    count = text.count(four_arg)
    if count != 1:
        raise SystemExit("StartCookCommandData 4-arg constructor anchor matched %d" % count)
    text = text.replace(four_arg, four_arg_new, 1)

    getter_anchor = """.method public getTemperatureCavity()I"""
    getter = """.method public getPlanIndex()I
    .locals 1

    iget v0, p0, Lcom/junelife/device/chef/webservices/model/StartCookCommandData;->planIndex:I

    return v0
.end method

"""
    count = text.count(getter_anchor)
    if count != 1:
        raise SystemExit("StartCookCommandData getter anchor matched %d" % count)
    text = text.replace(getter_anchor, getter + getter_anchor, 1)

    write_anchor = """    iget-object p2, p0, Lcom/junelife/device/chef/webservices/model/StartCookCommandData;->recipeId:Ljava/lang/String;

    invoke-virtual {p1, p2}, Landroid/os/Parcel;->writeString(Ljava/lang/String;)V

    return-void"""
    write_anchor_new = """    iget-object p2, p0, Lcom/junelife/device/chef/webservices/model/StartCookCommandData;->recipeId:Ljava/lang/String;

    invoke-virtual {p1, p2}, Landroid/os/Parcel;->writeString(Ljava/lang/String;)V

    iget p2, p0, Lcom/junelife/device/chef/webservices/model/StartCookCommandData;->planIndex:I

    invoke-virtual {p1, p2}, Landroid/os/Parcel;->writeInt(I)V

    return-void"""
    count = text.count(write_anchor)
    if count != 1:
        raise SystemExit("StartCookCommandData writeToParcel anchor matched %d" % count)
    text = text.replace(write_anchor, write_anchor_new, 1)

    write(path, text)
    print("[+] StartCookCommandData.plan_index added")


def patch_remote_food_load_listener(decoded):
    path = os.path.join(
        decoded,
        "smali/com/junelife/device/chef/services/foodloader/RemoteFoodLoadListener.smali",
    )
    text = read(path)
    if MARKER in text:
        print("[=] RemoteFoodLoadListener already patched")
        return

    field_anchor = (
        ".field private final mRecipeSessionId:Ljava/lang/String;\n\n"
    )
    field = (
        field_anchor
        + "# June Local: cook-modes\n"
        + ".field private mPlanIndex:I\n\n"
    )
    count = text.count(field_anchor)
    if count != 1:
        raise SystemExit("RemoteFoodLoadListener field anchor matched %d" % count)
    text = text.replace(field_anchor, field, 1)

    parcel_tail = """    invoke-virtual {p1}, Landroid/os/Parcel;->readString()Ljava/lang/String;

    move-result-object p1

    iput-object p1, p0, Lcom/junelife/device/chef/services/foodloader/RemoteFoodLoadListener;->mRecipeSessionId:Ljava/lang/String;

    return-void"""
    parcel_tail_new = """    invoke-virtual {p1}, Landroid/os/Parcel;->readString()Ljava/lang/String;

    move-result-object v0

    iput-object v0, p0, Lcom/junelife/device/chef/services/foodloader/RemoteFoodLoadListener;->mRecipeSessionId:Ljava/lang/String;

    # June Local: cook-modes
    invoke-virtual {p1}, Landroid/os/Parcel;->readInt()I

    move-result v0

    iput v0, p0, Lcom/junelife/device/chef/services/foodloader/RemoteFoodLoadListener;->mPlanIndex:I

    return-void"""
    count = text.count(parcel_tail)
    if count != 1:
        raise SystemExit("RemoteFoodLoadListener parcel anchor matched %d" % count)
    text = text.replace(parcel_tail, parcel_tail_new, 1)

    six_arg_tail = """    .line 89
    iput-object p6, p0, Lcom/junelife/device/chef/services/foodloader/RemoteFoodLoadListener;->mRecipeSessionId:Ljava/lang/String;

    return-void
.end method"""
    six_arg_tail_new = """    .line 89
    iput-object p6, p0, Lcom/junelife/device/chef/services/foodloader/RemoteFoodLoadListener;->mRecipeSessionId:Ljava/lang/String;

    const/4 v0, -0x1

    iput v0, p0, Lcom/junelife/device/chef/services/foodloader/RemoteFoodLoadListener;->mPlanIndex:I

    return-void
.end method

.method public constructor <init>(Lcom/junelife/device/chef/oven/CookStepController;Lcom/junelife/device/chef/oven/RecipeController;Lcom/junelife/device/chef/ui/cook/PreCookController;Lcom/junelife/device/chef/dispatch/RemoteCookCommand;Ljava/lang/String;Ljava/lang/String;I)V
    .locals 0

    # June Local: cook-modes. plan index is 0-based; -1 keeps the stock plan.
    invoke-direct/range {p0 .. p6}, Lcom/junelife/device/chef/services/foodloader/RemoteFoodLoadListener;-><init>(Lcom/junelife/device/chef/oven/CookStepController;Lcom/junelife/device/chef/oven/RecipeController;Lcom/junelife/device/chef/ui/cook/PreCookController;Lcom/junelife/device/chef/dispatch/RemoteCookCommand;Ljava/lang/String;Ljava/lang/String;)V

    iput p7, p0, Lcom/junelife/device/chef/services/foodloader/RemoteFoodLoadListener;->mPlanIndex:I

    return-void
.end method"""
    count = text.count(six_arg_tail)
    if count != 1:
        raise SystemExit("RemoteFoodLoadListener 6-arg constructor anchor matched %d" % count)
    text = text.replace(six_arg_tail, six_arg_tail_new, 1)
    text = text.replace(
        """.method public constructor <init>(Lcom/junelife/device/chef/oven/CookStepController;Lcom/junelife/device/chef/oven/RecipeController;Lcom/junelife/device/chef/ui/cook/PreCookController;Lcom/junelife/device/chef/dispatch/RemoteCookCommand;Ljava/lang/String;Ljava/lang/String;)V
    .locals 0""",
        """.method public constructor <init>(Lcom/junelife/device/chef/oven/CookStepController;Lcom/junelife/device/chef/oven/RecipeController;Lcom/junelife/device/chef/ui/cook/PreCookController;Lcom/junelife/device/chef/dispatch/RemoteCookCommand;Ljava/lang/String;Ljava/lang/String;)V
    .locals 1""",
        1,
    )

    finish_anchor = """    :cond_0
    invoke-virtual {p2}, Lcom/junelife/device/chef/data/model/Food;->getSelectedPlan()Lcom/junelife/device/chef/data/model/Plan;"""
    finish_new = """    :cond_0
    # June Local: cook-modes. Apply the requested plan (if any) before the
    # stock selected-plan checks; otherwise selectPlan(0) from the loader wins.
    iget v2, p0, Lcom/junelife/device/chef/services/foodloader/RemoteFoodLoadListener;->mPlanIndex:I

    if-ltz v2, :plan_selected

    invoke-virtual {p2}, Lcom/junelife/device/chef/data/model/Food;->getPlans()Ljava/util/List;

    move-result-object v3

    invoke-interface {v3}, Ljava/util/List;->size()I

    move-result v3

    if-ge v2, v3, :plan_bad

    invoke-virtual {p2, v2}, Lcom/junelife/device/chef/data/model/Food;->selectPlan(I)Lcom/junelife/device/chef/data/model/Plan;

    move-result-object v2

    goto :plan_selected

    :plan_bad
    new-array p1, v1, [Ljava/lang/Object;

    const-string p2, "Requested plan index is out of range."

    invoke-static {v0, p2, p1}, Lcom/junelife/device/common/logging/Logr;->w(Ljava/lang/String;Ljava/lang/String;[Ljava/lang/Object;)V

    iget-object p1, p0, Lcom/junelife/device/chef/services/foodloader/RemoteFoodLoadListener;->mRecipeController:Lcom/junelife/device/chef/oven/RecipeController;

    iget-object p2, p0, Lcom/junelife/device/chef/services/foodloader/RemoteFoodLoadListener;->mRemoteCommand:Lcom/junelife/device/chef/dispatch/RemoteCookCommand;

    invoke-virtual {p1, p2}, Lcom/junelife/device/chef/oven/RecipeController;->sendAckNotAllowed(Lcom/junelife/device/chef/dispatch/RemoteCookCommand;)V

    return-void

    :plan_selected
    invoke-virtual {p2}, Lcom/junelife/device/chef/data/model/Food;->getSelectedPlan()Lcom/junelife/device/chef/data/model/Plan;"""
    count = text.count(finish_anchor)
    if count != 1:
        raise SystemExit("RemoteFoodLoadListener finishFood anchor matched %d" % count)
    text = text.replace(finish_anchor, finish_new, 1)

    write_tail = """    iget-object p2, p0, Lcom/junelife/device/chef/services/foodloader/RemoteFoodLoadListener;->mRecipeSessionId:Ljava/lang/String;

    invoke-virtual {p1, p2}, Landroid/os/Parcel;->writeString(Ljava/lang/String;)V

    return-void"""
    write_tail_new = """    iget-object p2, p0, Lcom/junelife/device/chef/services/foodloader/RemoteFoodLoadListener;->mRecipeSessionId:Ljava/lang/String;

    invoke-virtual {p1, p2}, Landroid/os/Parcel;->writeString(Ljava/lang/String;)V

    iget p2, p0, Lcom/junelife/device/chef/services/foodloader/RemoteFoodLoadListener;->mPlanIndex:I

    invoke-virtual {p1, p2}, Landroid/os/Parcel;->writeInt(I)V

    return-void"""
    count = text.count(write_tail)
    if count != 1:
        raise SystemExit("RemoteFoodLoadListener writeToParcel anchor matched %d" % count)
    text = text.replace(write_tail, write_tail_new, 1)

    write(path, text)
    print("[+] RemoteFoodLoadListener.plan_index added")


def patch_remote_command_receiver(decoded):
    path = os.path.join(
        decoded,
        "smali/com/junelife/device/chef/broadcastreceivers/RemoteCommandReceiver.smali",
    )
    text = read(path)
    if MARKER in text:
        print("[=] RemoteCommandReceiver already patched")
        return

    old = """    :cond_1
    new-instance v9, Lcom/junelife/device/chef/services/foodloader/RemoteFoodLoadListener;

    .line 492
    iget-object v3, p0, Lcom/junelife/device/chef/broadcastreceivers/RemoteCommandReceiver;->cookStepController:Lcom/junelife/device/chef/oven/CookStepController;

    .line 493
    iget-object v4, p0, Lcom/junelife/device/chef/broadcastreceivers/RemoteCommandReceiver;->recipeController:Lcom/junelife/device/chef/oven/RecipeController;

    .line 494
    iget-object v5, p0, Lcom/junelife/device/chef/broadcastreceivers/RemoteCommandReceiver;->preCookController:Lcom/junelife/device/chef/ui/cook/PreCookController;

    .line 496
    invoke-virtual {v0}, Lcom/junelife/device/chef/webservices/model/StartCookCommandData;->getRecipeId()Ljava/lang/String;

    move-result-object v7

    .line 497
    invoke-virtual {v0}, Lcom/junelife/device/chef/webservices/model/StartCookCommandData;->getRecipeSessionId()Ljava/lang/String;

    move-result-object v8

    move-object v2, v9

    move-object v6, p2

    .line 491
    invoke-direct/range {v2 .. v8}, Lcom/junelife/device/chef/services/foodloader/RemoteFoodLoadListener;-><init>(Lcom/junelife/device/chef/oven/CookStepController;Lcom/junelife/device/chef/oven/RecipeController;Lcom/junelife/device/chef/ui/cook/PreCookController;Lcom/junelife/device/chef/dispatch/RemoteCookCommand;Ljava/lang/String;Ljava/lang/String;)V

    .line 499
    invoke-virtual {v0}, Lcom/junelife/device/chef/webservices/model/StartCookCommandData;->getFoodName()Ljava/lang/String;

    move-result-object p2

    check-cast v9, Lcom/junelife/device/chef/services/foodloader/FoodLoaderService$Finisher;

    invoke-static {p1, p2, v1, v9}, Lcom/junelife/device/chef/services/foodloader/FoodLoaderService;->startForPreheatPrimitive(Landroid/content/Context;Ljava/lang/String;ILcom/junelife/device/chef/services/foodloader/FoodLoaderService$Finisher;)Landroid/content/Intent;

    move-result-object p2"""
    new = """    :cond_1
    new-instance v2, Lcom/junelife/device/chef/services/foodloader/RemoteFoodLoadListener;

    .line 492
    iget-object v3, p0, Lcom/junelife/device/chef/broadcastreceivers/RemoteCommandReceiver;->cookStepController:Lcom/junelife/device/chef/oven/CookStepController;

    .line 493
    iget-object v4, p0, Lcom/junelife/device/chef/broadcastreceivers/RemoteCommandReceiver;->recipeController:Lcom/junelife/device/chef/oven/RecipeController;

    .line 494
    iget-object v5, p0, Lcom/junelife/device/chef/broadcastreceivers/RemoteCommandReceiver;->preCookController:Lcom/junelife/device/chef/ui/cook/PreCookController;

    move-object v6, p2

    .line 496
    invoke-virtual {v0}, Lcom/junelife/device/chef/webservices/model/StartCookCommandData;->getRecipeId()Ljava/lang/String;

    move-result-object v7

    .line 497
    invoke-virtual {v0}, Lcom/junelife/device/chef/webservices/model/StartCookCommandData;->getRecipeSessionId()Ljava/lang/String;

    move-result-object v8

    # June Local: cook-modes. Optional 0-based plan index for multi-plan
    # primitives such as toast (level N -> plan_index N-1).
    invoke-virtual {v0}, Lcom/junelife/device/chef/webservices/model/StartCookCommandData;->getPlanIndex()I

    move-result v9

    .line 491
    invoke-direct/range {v2 .. v9}, Lcom/junelife/device/chef/services/foodloader/RemoteFoodLoadListener;-><init>(Lcom/junelife/device/chef/oven/CookStepController;Lcom/junelife/device/chef/oven/RecipeController;Lcom/junelife/device/chef/ui/cook/PreCookController;Lcom/junelife/device/chef/dispatch/RemoteCookCommand;Ljava/lang/String;Ljava/lang/String;I)V

    .line 499
    invoke-virtual {v0}, Lcom/junelife/device/chef/webservices/model/StartCookCommandData;->getFoodName()Ljava/lang/String;

    move-result-object p2

    check-cast v2, Lcom/junelife/device/chef/services/foodloader/FoodLoaderService$Finisher;

    invoke-static {p1, p2, v1, v2}, Lcom/junelife/device/chef/services/foodloader/FoodLoaderService;->startForPreheatPrimitive(Landroid/content/Context;Ljava/lang/String;ILcom/junelife/device/chef/services/foodloader/FoodLoaderService$Finisher;)Landroid/content/Intent;

    move-result-object p2"""
    count = text.count(old)
    if count != 1:
        raise SystemExit("RemoteCommandReceiver primitive start anchor matched %d" % count)
    text = text.replace(old, new, 1)

    write(path, text)
    print("[+] RemoteCommandReceiver primitive start passes plan_index")


UPDATES_MARKER = "June Local: updates screen"
HELPER_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "smali")
LAUNCHER_DIR = "smali/com/junelife/device/chef/ui/launcher"

# The "Project July" tile. It is a launcher cell with a new mode id (0x30; the existing
# MODE_ID_* values top out at 0x25) and a literal label. The label is a smali constant
# because the build uses `apktool d -r`, so resources.arsc stays binary and no new string
# resource can be added.
PROJECT_JULY_METHOD = """.method public final projectJuly()Lcom/junelife/device/chef/ui/launcher/LauncherListBuilder;
    .locals 8

    # June Local: updates screen. Home-screen tile that opens june-local's
    # Updates page; LauncherActivity.onIconClicked handles mode 0x30.
    move-object v0, p0

    check-cast v0, Lcom/junelife/device/chef/ui/launcher/LauncherListBuilder;

    const/4 v1, 0x1

    new-array v1, v1, [Lcom/junelife/device/chef/ui/launcher/LauncherCell;

    new-instance v2, Lcom/junelife/device/chef/ui/launcher/LauncherCell$Icon;

    new-instance v3, Lcom/junelife/device/chef/ui/launcher/LauncherIconViewData;

    # launcher_chef (0x7f0800a5); launcher_settings (0x7f0800b0) is the fallback.
    new-instance v4, Lcom/junelife/device/commonui/image/DrawableImageResource;

    const v5, 0x7f0800a5

    invoke-direct {v4, v5}, Lcom/junelife/device/commonui/image/DrawableImageResource;-><init>(I)V

    check-cast v4, Lcom/junelife/device/commonui/image/ImageResource;

    # Literal label: the build keeps resources binary (apktool d -r).
    const-string v5, "Project July"

    iget-object v6, p0, Lcom/junelife/device/chef/ui/launcher/LauncherListBuilder;->cells:Ljava/util/List;

    invoke-interface {v6}, Ljava/util/List;->size()I

    move-result v6

    const/16 v7, 0x30

    invoke-direct {v3, v7, v4, v5, v6}, Lcom/junelife/device/chef/ui/launcher/LauncherIconViewData;-><init>(ILcom/junelife/device/commonui/image/ImageResource;Ljava/lang/String;I)V

    invoke-direct {v2, v3}, Lcom/junelife/device/chef/ui/launcher/LauncherCell$Icon;-><init>(Lcom/junelife/device/chef/ui/launcher/LauncherIconViewData;)V

    check-cast v2, Lcom/junelife/device/chef/ui/launcher/LauncherCell;

    const/4 v3, 0x0

    aput-object v2, v1, v3

    invoke-virtual {p0, v1}, Lcom/junelife/device/chef/ui/launcher/LauncherListBuilder;->add([Lcom/junelife/device/chef/ui/launcher/LauncherCell;)Lcom/junelife/device/chef/ui/launcher/LauncherListBuilder;

    return-object v0
.end method

"""

FILL_ANCHOR = """    invoke-virtual {p1}, Lcom/junelife/device/chef/ui/launcher/LauncherListBuilder;->settings()Lcom/junelife/device/chef/ui/launcher/LauncherListBuilder;

    move-result-object p1"""

FILL_INJECT = FILL_ANCHOR + """

    # June Local: updates screen. Project July tile (mode 0x30) after Settings.
    invoke-virtual {p1}, Lcom/junelife/device/chef/ui/launcher/LauncherListBuilder;->projectJuly()Lcom/junelife/device/chef/ui/launcher/LauncherListBuilder;

    move-result-object p1"""

ON_ICON_ANCHOR = """.method public onIconClicked(ILjava/lang/String;Ljava/lang/String;)V
    .locals 7

    const-string v0, "LauncherActivity\""""

ON_ICON_INJECT = """.method public onIconClicked(ILjava/lang/String;Ljava/lang/String;)V
    .locals 7

    # June Local: updates screen. Project July tile (mode 0x30) opens the
    # full-screen WebView. p0 is the LauncherActivity, so the Dialog gets a
    # valid window token. v4 is overwritten immediately by the stock code.
    const/16 v4, 0x30

    if-ne p1, v4, :not_project_july

    invoke-static {p0}, Lcom/projectjuly/chef/OvenUpdatesDialog;->show(Landroid/content/Context;)V

    return-void

    :not_project_july
    const-string v0, "LauncherActivity\""""


def add_updates_screen(decoded):
    """Adds the OvenUpdatesDialog classes in their own dex.

    The home-screen tile that opens them is added by patch_launcher_tile().
    """
    dirs = sorted(d for d in os.listdir(decoded) if d == "smali" or d.startswith("smali_classes"))
    for d in dirs:
        if os.path.exists(os.path.join(decoded, d, "com/projectjuly/chef/OvenUpdatesDialog.smali")):
            print("[=] updates screen classes already present in %s" % d)
            break
    else:
        # A fresh smali_classesN keeps the helper out of the (possibly full) primary dex.
        n = 2
        while "smali_classes%d" % n in dirs:
            n += 1
        dest = os.path.join(decoded, "smali_classes%d" % n, "com/projectjuly/chef")
        os.makedirs(dest)
        src = os.path.join(HELPER_DIR, "com/projectjuly/chef")
        for name in sorted(os.listdir(src)):
            write(os.path.join(dest, name), read(os.path.join(src, name)))
        print("[+] updates screen classes added as smali_classes%d" % n)


def patch_launcher_tile(decoded):
    """Adds the "Project July" home-screen tile and its click handler.

    Product decision: the real Settings screen is a separate app
    (JuneSettings.apk), so a Chef-only patch cannot add a row there. The tile
    lives in Chef's own launcher instead. Each injected region carries
    UPDATES_MARKER, so re-running the patch skips files already patched.
    """
    builder = os.path.join(decoded, LAUNCHER_DIR, "LauncherListBuilder.smali")
    text = read(builder)
    if UPDATES_MARKER in text:
        print("[=] LauncherListBuilder.projectJuly already present")
    else:
        anchor = ".method public final settings()Lcom/junelife/device/chef/ui/launcher/LauncherListBuilder;\n"
        count = text.count(anchor)
        if count != 1:
            raise SystemExit("LauncherListBuilder settings anchor matched %d" % count)
        write(builder, text.replace(anchor, PROJECT_JULY_METHOD + anchor, 1))
        print("[+] LauncherListBuilder.projectJuly added (mode 0x30, icon 0x7f0800a5)")

    for name in ("Dragon", "Meerkat"):
        path = os.path.join(decoded, LAUNCHER_DIR, "LauncherListSource$%s.smali" % name)
        text = read(path)
        if UPDATES_MARKER in text:
            print("[=] LauncherListSource$%s.fill already calls projectJuly" % name)
            continue
        count = text.count(FILL_ANCHOR)
        if count != 1:
            raise SystemExit(
                "LauncherListSource$%s fill anchor matched %d" % (name, count)
            )
        write(path, text.replace(FILL_ANCHOR, FILL_INJECT, 1))
        print("[+] LauncherListSource$%s.fill calls projectJuly after settings" % name)

    path = os.path.join(decoded, LAUNCHER_DIR, "LauncherActivity.smali")
    text = read(path)
    if UPDATES_MARKER in text:
        print("[=] LauncherActivity.onIconClicked already handles mode 0x30")
    else:
        if ":not_project_july" in text:
            raise SystemExit("LauncherActivity already has a :not_project_july label")
        count = text.count(ON_ICON_ANCHOR)
        if count != 1:
            raise SystemExit(
                "LauncherActivity onIconClicked anchor matched %d" % count
            )
        write(path, text.replace(ON_ICON_ANCHOR, ON_ICON_INJECT, 1))
        print("[+] LauncherActivity.onIconClicked opens the updates screen for mode 0x30")


def main():
    if len(sys.argv) != 2:
        raise SystemExit(__doc__)
    decoded = sys.argv[1]
    if not os.path.isdir(os.path.join(decoded, "smali")):
        raise SystemExit("%s does not look like an apktool-decoded tree" % decoded)
    patch_validator(decoded)
    patch_start_cook_command_data(decoded)
    patch_remote_food_load_listener(decoded)
    patch_remote_command_receiver(decoded)
    add_updates_screen(decoded)
    patch_launcher_tile(decoded)
    print("[+] Chef cook-modes patch complete")


if __name__ == "__main__":
    main()
