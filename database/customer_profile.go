// database/customer_profiles.go - Novos métodos para gerenciar Customer Profiles
package database

import (
    "context"
    "database/sql"
    "fmt"
    "log"
    "time"
)

// CustomerProfileData representa os dados de um Customer Profile
type CustomerProfileData struct {
    ID                          int       `json:"id"`
    MasterReference            string    `json:"master_reference"`
    AuthorizeCustomerProfileID string    `json:"authorize_customer_profile_id"`
    AuthorizePaymentProfileID  string    `json:"authorize_payment_profile_id"`
    CreatedAt                  time.Time `json:"created_at"`
    UpdatedAt                  time.Time `json:"updated_at"`
}

// SaveCustomerProfile salva ou atualiza um Customer Profile no banco de dados
func (c *Connection) SaveCustomerProfile(masterRef, customerProfileID, paymentProfileID string) error {
    if err := c.ensureConnection(); err != nil {
        return fmt.Errorf("database connection check failed: %v", err)
    }

    log.Printf("Saving customer profile for master reference: %s", masterRef)
    
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    query := `
        INSERT INTO customer_profiles 
        (master_reference, authorize_customer_profile_id, authorize_payment_profile_id, created_at)
        VALUES (?, ?, ?, NOW())
        ON DUPLICATE KEY UPDATE
        authorize_customer_profile_id = VALUES(authorize_customer_profile_id),
        authorize_payment_profile_id = VALUES(authorize_payment_profile_id),
        updated_at = NOW()
    `
   
    result, err := c.db.ExecContext(ctx, query, masterRef, customerProfileID, paymentProfileID)
    if err != nil {
        log.Printf("Error saving customer profile: %v", err)
        return fmt.Errorf("error saving customer profile: %v", err)
    }

    rowsAffected, err := result.RowsAffected()
    if err != nil {
        return fmt.Errorf("error getting rows affected: %v", err)
    }

    if rowsAffected > 0 {
        log.Printf("Successfully saved customer profile for master reference: %s", masterRef)
    }
    
    return nil
}

// GetCustomerProfile busca um Customer Profile por master reference
func (c *Connection) GetCustomerProfile(masterRef string) (*CustomerProfileData, error) {
    if err := c.ensureConnection(); err != nil {
        return nil, fmt.Errorf("database connection check failed: %v", err)
    }

    log.Printf("Getting customer profile for master reference: %s", masterRef)
    
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    query := `
        SELECT id, master_reference, authorize_customer_profile_id, 
               authorize_payment_profile_id, created_at, updated_at
        FROM customer_profiles 
        WHERE master_reference = ?
    `
    
    var profile CustomerProfileData
    err := c.db.QueryRowContext(ctx, query, masterRef).Scan(
        &profile.ID,
        &profile.MasterReference,
        &profile.AuthorizeCustomerProfileID,
        &profile.AuthorizePaymentProfileID,
        &profile.CreatedAt,
        &profile.UpdatedAt,
    )
    
    if err != nil {
        if err == sql.ErrNoRows {
            return nil, fmt.Errorf("no customer profile found for master reference: %s", masterRef)
        }
        log.Printf("Error getting customer profile: %v", err)
        return nil, fmt.Errorf("error getting customer profile: %v", err)
    }

    log.Printf("Successfully retrieved customer profile for master reference: %s", masterRef)
    return &profile, nil
}

// GetCustomerProfileByEmail busca um Customer Profile por email
func (c *Connection) GetCustomerProfileByEmail(email string) (*CustomerProfileData, error) {
    if err := c.ensureConnection(); err != nil {
        return nil, fmt.Errorf("database connection check failed: %v", err)
    }

    log.Printf("Getting customer profile for email: %s", email)
    
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    query := `
        SELECT cp.id, cp.master_reference, cp.authorize_customer_profile_id, 
               cp.authorize_payment_profile_id, cp.created_at, cp.updated_at
        FROM customer_profiles cp
        JOIN master_accounts ma ON cp.master_reference = ma.reference_uuid
        WHERE ma.email = ?
    `
    
    var profile CustomerProfileData
    err := c.db.QueryRowContext(ctx, query, email).Scan(
        &profile.ID,
        &profile.MasterReference,
        &profile.AuthorizeCustomerProfileID,
        &profile.AuthorizePaymentProfileID,
        &profile.CreatedAt,
        &profile.UpdatedAt,
    )
    
    if err != nil {
        if err == sql.ErrNoRows {
            return nil, fmt.Errorf("no customer profile found for email: %s", email)
        }
        log.Printf("Error getting customer profile by email: %v", err)
        return nil, fmt.Errorf("error getting customer profile by email: %v", err)
    }

    log.Printf("Successfully retrieved customer profile for email: %s", email)
    return &profile, nil
}

// GetCustomerProfileByUsername busca um Customer Profile por username
func (c *Connection) GetCustomerProfileByUsername(username string) (*CustomerProfileData, error) {
    if err := c.ensureConnection(); err != nil {
        return nil, fmt.Errorf("database connection check failed: %v", err)
    }

    log.Printf("Getting customer profile for username: %s", username)
    
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    query := `
        SELECT cp.id, cp.master_reference, cp.authorize_customer_profile_id, 
               cp.authorize_payment_profile_id, cp.created_at, cp.updated_at
        FROM customer_profiles cp
        JOIN master_accounts ma ON cp.master_reference = ma.reference_uuid
        WHERE ma.username = ?
    `
    
    var profile CustomerProfileData
    err := c.db.QueryRowContext(ctx, query, username).Scan(
        &profile.ID,
        &profile.MasterReference,
        &profile.AuthorizeCustomerProfileID,
        &profile.AuthorizePaymentProfileID,
        &profile.CreatedAt,
        &profile.UpdatedAt,
    )
    
    if err != nil {
        if err == sql.ErrNoRows {
            return nil, fmt.Errorf("no customer profile found for username: %s", username)
        }
        log.Printf("Error getting customer profile by username: %v", err)
        return nil, fmt.Errorf("error getting customer profile by username: %v", err)
    }

    log.Printf("Successfully retrieved customer profile for username: %s", username)
    return &profile, nil
}

// DeleteCustomerProfile remove um Customer Profile do banco de dados
func (c *Connection) DeleteCustomerProfile(masterRef string) error {
    if err := c.ensureConnection(); err != nil {
        return fmt.Errorf("database connection check failed: %v", err)
    }

    log.Printf("Deleting customer profile for master reference: %s", masterRef)
    
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    query := `DELETE FROM customer_profiles WHERE master_reference = ?`
   
    result, err := c.db.ExecContext(ctx, query, masterRef)
    if err != nil {
        log.Printf("Error deleting customer profile: %v", err)
        return fmt.Errorf("error deleting customer profile: %v", err)
    }

    rowsAffected, err := result.RowsAffected()
    if err != nil {
        return fmt.Errorf("error getting rows affected: %v", err)
    }

    if rowsAffected == 0 {
        return fmt.Errorf("no customer profile found with master reference %s", masterRef)
    }

    log.Printf("Successfully deleted customer profile for master reference: %s", masterRef)
    return nil
}

// ListCustomerProfiles lista todos os Customer Profiles (para admin/debug)
func (c *Connection) ListCustomerProfiles(limit, offset int) ([]CustomerProfileData, error) {
    if err := c.ensureConnection(); err != nil {
        return nil, fmt.Errorf("database connection check failed: %v", err)
    }

    log.Printf("Listing customer profiles with limit: %d, offset: %d", limit, offset)
    
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    query := `
        SELECT cp.id, cp.master_reference, cp.authorize_customer_profile_id, 
               cp.authorize_payment_profile_id, cp.created_at, cp.updated_at
        FROM customer_profiles cp
        ORDER BY cp.created_at DESC
        LIMIT ? OFFSET ?
    `
    
    rows, err := c.db.QueryContext(ctx, query, limit, offset)
    if err != nil {
        log.Printf("Error listing customer profiles: %v", err)
        return nil, fmt.Errorf("error listing customer profiles: %v", err)
    }
    defer rows.Close()

    var profiles []CustomerProfileData
    for rows.Next() {
        var profile CustomerProfileData
        err := rows.Scan(
            &profile.ID,
            &profile.MasterReference,
            &profile.AuthorizeCustomerProfileID,
            &profile.AuthorizePaymentProfileID,
            &profile.CreatedAt,
            &profile.UpdatedAt,
        )
        if err != nil {
            log.Printf("Error scanning customer profile row: %v", err)
            continue
        }
        profiles = append(profiles, profile)
    }

    if err = rows.Err(); err != nil {
        return nil, fmt.Errorf("error iterating customer profile rows: %v", err)
    }

    log.Printf("Successfully retrieved %d customer profiles", len(profiles))
    return profiles, nil
}

// CountCustomerProfiles conta o total de Customer Profiles
func (c *Connection) CountCustomerProfiles() (int, error) {
    if err := c.ensureConnection(); err != nil {
        return 0, fmt.Errorf("database connection check failed: %v", err)
    }

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    var count int
    query := `SELECT COUNT(*) FROM customer_profiles`
    
    err := c.db.QueryRowContext(ctx, query).Scan(&count)
    if err != nil {
        log.Printf("Error counting customer profiles: %v", err)
        return 0, fmt.Errorf("error counting customer profiles: %v", err)
    }

    return count, nil
}

// UpdateCustomerProfileIDs atualiza os IDs do Customer Profile
func (c *Connection) UpdateCustomerProfileIDs(masterRef, newCustomerProfileID, newPaymentProfileID string) error {
    if err := c.ensureConnection(); err != nil {
        return fmt.Errorf("database connection check failed: %v", err)
    }

    log.Printf("Updating customer profile IDs for master reference: %s", masterRef)
    
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    query := `
        UPDATE customer_profiles 
        SET authorize_customer_profile_id = ?, 
            authorize_payment_profile_id = ?,
            updated_at = NOW()
        WHERE master_reference = ?
    `
   
    result, err := c.db.ExecContext(ctx, query, newCustomerProfileID, newPaymentProfileID, masterRef)
    if err != nil {
        log.Printf("Error updating customer profile IDs: %v", err)
        return fmt.Errorf("error updating customer profile IDs: %v", err)
    }

    rowsAffected, err := result.RowsAffected()
    if err != nil {
        return fmt.Errorf("error getting rows affected: %v", err)
    }

    if rowsAffected == 0 {
        return fmt.Errorf("no customer profile found with master reference %s", masterRef)
    }

    log.Printf("Successfully updated customer profile IDs for master reference: %s", masterRef)
    return nil
}

// GetCustomerProfileStats retorna estatísticas dos Customer Profiles
func (c *Connection) GetCustomerProfileStats() (map[string]interface{}, error) {
    if err := c.ensureConnection(); err != nil {
        return nil, fmt.Errorf("database connection check failed: %v", err)
    }

    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    stats := make(map[string]interface{})

    // Total de profiles
    var totalProfiles int
    err := c.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM customer_profiles").Scan(&totalProfiles)
    if err != nil {
        return nil, fmt.Errorf("error getting total profiles count: %v", err)
    }
    stats["total_profiles"] = totalProfiles

    // Profiles criados hoje
    var profilesToday int
    err = c.db.QueryRowContext(ctx, 
        "SELECT COUNT(*) FROM customer_profiles WHERE DATE(created_at) = CURDATE()").Scan(&profilesToday)
    if err != nil {
        return nil, fmt.Errorf("error getting today's profiles count: %v", err)
    }
    stats["profiles_created_today"] = profilesToday

    // Profiles criados esta semana
    var profilesThisWeek int
    err = c.db.QueryRowContext(ctx, 
        "SELECT COUNT(*) FROM customer_profiles WHERE created_at >= DATE_SUB(NOW(), INTERVAL 7 DAY)").Scan(&profilesThisWeek)
    if err != nil {
        return nil, fmt.Errorf("error getting this week's profiles count: %v", err)
    }
    stats["profiles_created_this_week"] = profilesThisWeek

    // Profiles atualizados hoje
    var profilesUpdatedToday int
    err = c.db.QueryRowContext(ctx, 
        "SELECT COUNT(*) FROM customer_profiles WHERE DATE(updated_at) = CURDATE() AND DATE(updated_at) != DATE(created_at)").Scan(&profilesUpdatedToday)
    if err != nil {
        return nil, fmt.Errorf("error getting updated profiles count: %v", err)
    }
    stats["profiles_updated_today"] = profilesUpdatedToday

    log.Printf("Customer Profile Stats: %+v", stats)
    return stats, nil
}