// Copyright 2026 The Outline Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package org.outline.vpn;

import android.content.Context;
import android.content.SharedPreferences;
import android.content.pm.PackageManager;
import android.net.VpnService;
import java.util.Collections;
import java.util.HashSet;
import java.util.Set;
import java.util.logging.Level;
import java.util.logging.Logger;

/** Per-app VPN policy shared by the Cordova UI process and the VPN process. */
public final class SplitTunnelingPreferences {
  private static final Logger LOG = Logger.getLogger(SplitTunnelingPreferences.class.getName());
  private static final String PREFERENCES_NAME = "split_tunneling";
  private static final String MODE_KEY = "mode";
  private static final String PACKAGES_KEY = "packages";

  public static final String MODE_ALL = "all";
  public static final String MODE_BYPASS = "bypass";
  public static final String MODE_ONLY = "only";

  private SplitTunnelingPreferences() {}

  private static SharedPreferences preferences(Context context) {
    return context.getSharedPreferences(PREFERENCES_NAME, Context.MODE_PRIVATE);
  }

  public static String getMode(Context context) {
    String mode = preferences(context).getString(MODE_KEY, MODE_ALL);
    return isValidMode(mode) ? mode : MODE_ALL;
  }

  public static Set<String> getPackages(Context context) {
    return new HashSet<>(preferences(context).getStringSet(PACKAGES_KEY, Collections.emptySet()));
  }

  public static void save(Context context, String mode, Set<String> packages) {
    if (!isValidMode(mode) || (MODE_ONLY.equals(mode) && packages.isEmpty())) {
      throw new IllegalArgumentException("Invalid split tunneling policy");
    }
    if (packages.contains(context.getPackageName())) {
      throw new IllegalArgumentException("The VPN app cannot be selected");
    }
    if (MODE_ONLY.equals(mode)) {
      boolean anyInstalled = false;
      for (String packageName : packages) {
        try {
          context.getPackageManager().getApplicationInfo(packageName, 0);
          anyInstalled = true;
          break;
        } catch (PackageManager.NameNotFoundException ignored) {
          // Keep looking for an installed application.
        }
      }
      if (!anyInstalled) {
        throw new IllegalArgumentException("Select an installed application");
      }
    }
    if (!preferences(context).edit()
        .putString(MODE_KEY, mode)
        .putStringSet(PACKAGES_KEY, new HashSet<>(packages))
        .commit()) {
      throw new IllegalStateException("Could not save split tunneling policy");
    }
  }

  public static void apply(
      Context context, VpnService.Builder builder, String mode, Set<String> packages) {
    if (MODE_ONLY.equals(mode) && !packages.isEmpty()) {
      boolean anyInstalled = false;
      for (String packageName : packages) {
        anyInstalled |= addApplication(builder, packageName, true);
      }
      if (!anyInstalled) {
        // An empty allowed list would route every app through the VPN on Android.
        throw new IllegalStateException("No selected VPN application is installed");
      }
    } else {
      // The Outline process needs a direct route to the server.
      addApplication(builder, context.getPackageName(), false);
      if (MODE_BYPASS.equals(mode)) {
        for (String packageName : packages) {
          addApplication(builder, packageName, false);
        }
      }
    }
  }

  private static boolean addApplication(
      VpnService.Builder builder, String packageName, boolean allowed) {
    try {
      if (allowed) {
        builder.addAllowedApplication(packageName);
      } else {
        builder.addDisallowedApplication(packageName);
      }
      return true;
    } catch (PackageManager.NameNotFoundException e) {
      // Keep the saved choice so it takes effect if the application is reinstalled.
      LOG.log(Level.INFO, "Split tunneling application is not installed: " + packageName, e);
      return false;
    }
  }

  private static boolean isValidMode(String mode) {
    return MODE_ALL.equals(mode) || MODE_BYPASS.equals(mode) || MODE_ONLY.equals(mode);
  }
}
